# gost

Gost is a Go implementation of the [StatsD](https://github.com/etsy/statsd/) daemon.

## Usage

Right now there's no great installation method; you'll have to install from source:

    $ go get github.com/cespare/gost

Run `gost` with a conf file.

    $ gost -conf /my/config.toml

By default it uses `conf.toml`. This repo includes a [`conf.toml`](conf.toml) that should get you started. It
has a lot of comments that explain what all the options are.

### Messages

Gost is largely statsd compatible and any statsd library you want should work with it out of the box. The main
API difference is that gauges cannot be delta values (they are always interpreted as absolute).

For completeness, here is a summary of the supported messages. All messages are sent via UDP to localhost on a
port configured by the `port` setting in the config file. Typically each message is a UDP packet, but multiple
messages can be sent in a single packet by separating them with `\n` characters.

There are two data types involved: **keys** and **values**. **keys** are ascii strings (see the Key Format
section below for details). **values** are human-printed floats:

    /^[+\-]?\d+(\.\d+)?$/

Counters have a sampling rate, which is the same format as a value. This tells gost that the counter is being
sampled at some rate, and gost multiplies the counter value by the reciprocal of the sampling rate to obtain
an estimate of the true value.

**Counters**

A counter records occurrences of some event, or other values that can be accumulated by summing them.

For each counter, gost records two metrics:

* `count`: the raw counts (scaled for sample rate)
* `rate`: the rate, per second

Syntax: `<key>:<value>|c(|@<sampling-rate>)?`

Examples:

    rails.requests:1|c
    page_hits:135|c|@0.1

**Timers**

Timers are for measuring the elapsed time of some operation. These are more complex than the other kinds of
stats. For each timer key, gost records the following metrics during each flush period:

* `timer.count`: the number of timer calls that have been recorded
* `timer.rate`: the rate at which timer calls came in, per second
* `timer.min`, `timer.max`: the min and max values of the timer during the flush interval
* `timer.mean`, `timer.median`, `timer.stdev`: the mean, median, and standard deviation, respectively, of
  the timer values during the flush interval
* `timer.sum`: the total sum of all timer values during the interval. This value, in concert with
  `timer.count`, can be used (by some other system) to compute mean values across flush buckets.

Syntax: `<key>:<value>|ms(|@<sampling-rate>)?`

Example: `s3_backup:1411|ms`

**Gauges**

A gauge is simply a value that varies over time.

Syntax: `<key>:<value>|g`

Example: `active_users:992|g`

**Sets**

A set records the unique occurrences of some value. The metric sent to graphite is the number of unique values
that were given under a particular key during a flush interval.

Syntax: `<key>:<value>|s`

Example: `user_id:135|s`

### Meta-stats

Gost sends back some stats about itself to graphite as well. This includes:

* `gost.bad_messages_seen`: a counter for the number of malformed messages gost has received
* `gost.packets_received`: a counter for the number of packets gost has read
* `gost.distinct_metrics_flushed`: a gauge for the number of stats sent to graphite during the previous flush

There are some other counters for various error conditions. Most of these also show up in the stdout of gost
if you use the `debug_logging = true` option in the configuration.

### OS stats

One nice feature of gost is that, if you're running on a Linux system, it can automatically send back some
info about the host. See [the configuration file](conf.toml) for how to set this up.

* Load averages for 1, 5, and 15 minutes; either as-is or divided by the number of CPUs for convenience
* Disk usage for any given filesystem path

### Debug interface

The `debug_port` setting controls the port of a local server that gost starts up for debugging. Gost will
print its (UDP) input and (Graphite) output via TCP to any client that connects to this port. So if you're using
`debug_port = 8126` as in the example config, then you can connect like this:

    $ telnet localhost 8126

and you will see gost's input and output. This is very handy for debugging. You may want to filter out just a
subset of the data; for instance:

    $ netcat localhost 8127 | grep '\[out\]' # just outbound messages

## Key Format

Gost message keys are formed from printable ascii characters with a few restrictions, listed below. The
maximum size of an accepted UDP packet (which usually contains one message but may contain several separated
by `\n`) is 10Kb; this sets the only limit on key length.

source char |             converted to              | reason
------------|---------------------------------------|-------
   newline  |                 error                 | newlines end gost messages
    `:`     |                 error                 | colons end gost keys
   space    |                  `_`                  | graphite uses space in its message format
    `/`     |                  `-`                  | graphite can't handle `/` (keys are filenames)
  `<`, `>`  |                removed                | graphite doesn't handle `<` (`>` excluded for symmetry)
    `*`     |                removed                | graphite uses `*` as a wildcard
  `[`, `]`  |                removed                | graphite uses `[...]` for char set matching
  `{`, `}`, |                removed                | graphite uses `{...}` for matching multiple items

Additionally, note that a trailing `.` on a key will be ignored by Graphite, so `foo.` is the same as `foo`.

## Differences with StatsD

* Statsd only allows keys matching `/^[a-zA-Z0-9\-_\.]+$/`; gost is more permissive (see Key Format, above).
* Gauges cannot be deltas; they must be absolute values.
* Timers don't return as much information as in statsd, and they're not customizable.
* gost can record os stats from the host and deliver them to graphite as well.
* The "meta-stats" gost sends back are different from StatsD (there are a lot fewer of them)
* Gost is very fast. It can handle several times the load statsd can before dropping messages.
