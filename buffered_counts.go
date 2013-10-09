package main

import (
	"bytes"
	"encoding/gob"
	"fmt"

	"math"
	"sort"
)

type BufferedCounts struct {
	Counts map[string]float64
	Gauges map[string]float64
	Sets   map[string]map[float64]struct{}
	Timers map[string][]float64

	// When clear_stats_between_flushes = false, this is used to preserve {count, gauge, set} names between
	// flushes.
	PersistentKeys map[string]map[string]struct{}
}

func NewBufferedCounts() *BufferedCounts {
	return &BufferedCounts{
		Counts: make(map[string]float64),
		Gauges: make(map[string]float64),
		Sets:   make(map[string]map[float64]struct{}),
		Timers: make(map[string][]float64),
		PersistentKeys: map[string]map[string]struct{}{
			"count": make(map[string]struct{}),
			"set":   make(map[string]struct{}),
		},
	}
}

func (c *BufferedCounts) AddCount(key string, delta float64) { c.Counts[key] += delta }
func (c *BufferedCounts) SetGauge(key string, value float64) { c.Gauges[key] = value }

func (c *BufferedCounts) AddSetItem(key string, item float64) {
	set, ok := c.Sets[key]
	if ok {
		set[item] = struct{}{}
	} else {
		c.Sets[key] = map[float64]struct{}{item: {}}
	}
}

func (c *BufferedCounts) RecordTimer(key string, value float64) {
	c.Timers[key] = append(c.Timers[key], value)
}

// Merge merges in another BufferedCounts. Right now it only adds in Counts (because only counts can be
// forwarded).
func (c *BufferedCounts) Merge(c2 *BufferedCounts) {
	for name, value := range c2.Counts {
		c.AddCount(name, value)
	}
}

// computeDerived post-processes the stats to add in the derived metrics and returns map of all the key-value
// stats grouped by type.
func (c *BufferedCounts) computeDerived() map[string]map[string]float64 {
	result := map[string]map[string]float64{
		// Put in the stats we've already got
		"count": c.Counts,
		"gauge": c.Gauges,
	}

	// Empty maps for values to fill in
	for _, k := range []string{"rate", "set"} {
		result[k] = make(map[string]float64)
	}
	for _, k := range []string{"count", "rate", "sum", "mean", "stdev", "median", "min", "max"} {
		result["timer."+k] = make(map[string]float64)
	}

	// Compute the per-second rate for each counter
	rateFactor := float64(conf.FlushIntervalMS) / 1000
	for key, value := range c.Counts {
		result["rate"][key] = value / rateFactor
	}

	// Get the size of each set
	for key, value := range c.Sets {
		result["set"][key] = float64(len(value))
	}

	// Process all the various stats for each timer
	for key, values := range c.Timers {
		if len(values) == 0 {
			continue
		}
		// Useful for order statistics (technically there are faster algorithms though)
		sort.Float64s(values)
		count := float64(len(values))
		result["timer.count"][key] = count
		// rate is the rate (per second) at which timings were recorded.
		result["timer.rate"][key] = count / rateFactor
		// sum is the total sum of all timings. You can use count and sum to compute statistics across buckets.
		sum := 0.0
		for _, t := range values {
			sum += t
		}
		result["timer.sum"][key] = sum
		mean := sum / count
		result["timer.mean"][key] = mean
		sumSquares := 0.0
		for _, v := range values {
			d := v - mean
			sumSquares += d * d
		}
		result["timer.stdev"][key] = math.Sqrt(sumSquares / count)
		result["timer.min"][key] = values[0]
		result["timer.max"][key] = values[len(values)-1]
		if len(values)%2 == 0 {
			result["timer.median"][key] = float64(values[len(values)/2-1]+values[len(values)/2]) / 2
		} else {
			result["timer.median"][key] = float64(values[len(values)/2])
		}
	}

	// Add in any keys in PersistentKeys which don't already have values.
	for typ, keys := range c.PersistentKeys {
		for k := range keys {
			if _, ok := result[typ][k]; !ok {
				result[typ][k] = 0.0
			}
		}
	}

	return result
}

// CreateForwardMessage buffers up stats for forwarding to another gost instance. Right now it only serializes
// the counts, because they are all that may be forwarded.
// TODO: We could switch to a simple binary wire format to avoid reflection if gob ends up being a bottleneck.
func (c *BufferedCounts) CreateForwardMessage() (n int, msg []byte) {
	buf := &bytes.Buffer{}
	encoder := gob.NewEncoder(buf)
	if err := encoder.Encode(c.Counts); err != nil {
		dbg.Println("Error: Could not serialize forwarded message:", err)
		return 0, nil
	}
	return len(c.Counts), buf.Bytes()
}

// CreateGraphiteMessage buffers up a graphite message. c should not be used after calling this method.
// namespace is applied to all the keys; countGaugeName is the name of a counter appended to the message that
// lists the number of keys written. n is the number of keys written in total and msg is the graphite message
// ready to send.
// NOTE: We could write directly to the connection and avoid the extra buffering but this allows us to use
// separate goroutines to write to graphite (potentially slow) and aggregate (happening all the time).
func (c *BufferedCounts) CreateGraphiteMessage(namespace, countGaugeName string) (n int, msg []byte) {
	metrics := c.computeDerived()
	buf := &bytes.Buffer{}
	timestamp := now().Unix()
	for typ, s := range metrics {
		for key, value := range s {
			n++
			fmt.Fprintf(buf, "%s.%s.%s %f %d\n", namespace, key, typ, value, timestamp)
		}
	}
	n++
	fmt.Fprintf(buf, "%s.gost.%s.gauge %f %d\n", namespace, countGaugeName, float64(n), timestamp)
	return n, buf.Bytes()
}

// clearStats resets the state of all the stat types.
// - Counters and sets are deleted, but their names are recorded if persistStats = true.
// - Timers are always cleared because there aren't great semantics for persisting them.
// - Gauges are preserved as-is unless persistStats = false so they keep their current values.
func (c *BufferedCounts) Clear(persistStats bool) {
	if persistStats {
		for k := range c.Counts {
			c.PersistentKeys["count"][k] = struct{}{}
		}
		for k := range c.Sets {
			c.PersistentKeys["set"][k] = struct{}{}
		}
	} else {
		c.Gauges = make(map[string]float64)
	}
	c.Timers = make(map[string][]float64)
	c.Counts = make(map[string]float64)
	c.Sets = make(map[string]map[float64]struct{})
}
