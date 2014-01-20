package main

import (
	"fmt"
	"runtime"
	"syscall"
	"time"

	proc "github.com/cespare/goproc"
)

var (
	loadAvgTypes = []int{1, 5, 15}
	nCPU         = float64(runtime.NumCPU())
)

func osGauge(name string, value float64) {
	incoming <- &Stat{
		Type:       StatGauge,
		Name:       "gost.os_stats." + name,
		Value:      value,
		SampleRate: 1.0,
	}
}

func reportLoadAverages() {
	if !conf.OsStats.LoadAvg && !conf.OsStats.LoadAvgPerCPU {
		return
	}
	loadAverages, err := proc.LoadAverages()
	if err != nil {
		metaCount("load_avg_check_failures")
		dbg.Println("failed to check OS load average:", err)
		return
	}
	if conf.OsStats.LoadAvg {
		for i, avg := range loadAverages {
			osGauge(fmt.Sprintf("load_avg_%d", loadAvgTypes[i]), avg)
		}
	}
	if conf.OsStats.LoadAvgPerCPU {
		for i, avg := range loadAverages {
			osGauge(fmt.Sprintf("load_avg_per_cpu_%d", loadAvgTypes[i]), avg/nCPU)
		}
	}
}

func reportDiskUsage() {
	for name, options := range conf.OsStats.DiskUsage {
		buf := &syscall.Statfs_t{}
		if err := syscall.Statfs(options.Path, buf); err != nil {
			metaCount("disk_usage_check_failure")
			dbg.Printf("failed to check OS disk usage for %s at path %s\n", name, options.Path)
			return
		}
		usedBlocks := buf.Blocks - buf.Bavail // blocks used
		var used float64
		if options.Values == "absolute" {
			used = float64(usedBlocks * uint64(buf.Bsize)) // number of bytes used
		} else {
			used = float64(usedBlocks) / float64(buf.Blocks) // fraction of space used
		}
		osGauge("disk_usage."+name, used)
	}
}

func checkOsStats() {
	ticker := time.NewTicker(time.Duration(conf.OsStats.CheckIntervalMS) * time.Millisecond)
	for _ = range ticker.C {
		reportLoadAverages()
		reportDiskUsage()
	}
}
