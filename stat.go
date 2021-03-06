package imageproxy

import (
	"fmt"
	"os"
	"github.com/wojtekzw/statsd"
	"github.com/shirou/gopsutil/process"
)

func processMemStats(pid int32) (*process.MemoryInfoStat, error) {
	proc, err := process.NewProcess(pid)
	if err != nil {
		return nil, err
	}
	mem, _ := proc.MemoryInfo()

	return mem, nil
}

func printProcessMemStatsWithPrefix(prefix string) {
	m, e := processMemStats(int32(os.Getpid()))
	if e != nil {
		return
	}

	fmt.Printf("%s%v\n", prefix, m)
}

func printProcessMemStats() {
	printProcessMemStatsWithPrefix("")
}

func statsdProcessMemStats(c statsd.Statser) (*process.MemoryInfoStat) {
	m, e := processMemStats(int32(os.Getpid()))
	if e != nil {
		return  nil
	}

	c.Gauge("memory.rss", m.RSS)
	c.Gauge("memory.vms", m.VMS)
	c.Gauge("memory.swap", m.Swap)

	return m

}

func statsdTestMetrics(c statsd.Statser) {
	c.Gauge("test.metric.gauge", 1)
	c.Count("test.metric.count", 1)
	c.Timing("test.metric.timing", 1)
}

func statsdMaxBufferLen(c statsd.Statser) {
	c.Gauge("internal.statsd.maxbuflen", statsd.Debug.MaxBufferLen)

}
