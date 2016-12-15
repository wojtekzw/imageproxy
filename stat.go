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

func printProcessMemStats() {
	m, e := processMemStats(int32(os.Getpid()))
	if e != nil {
		return
	}

	fmt.Printf("%v\n", m)
}

func statsdProcessMemStats(c statsd.Statser) {
	m, e := processMemStats(int32(os.Getpid()))
	if e != nil {
		return
	}

	c.Gauge("memory.rss", m.RSS)
	c.Gauge("memory.vms", m.VMS)
	c.Gauge("memory.swap", m.Swap)

}

func statsdTestMetrics(c statsd.Statser) {
	c.Gauge("test.metric.gauge", 1)
	c.Count("test.metric.count", 1)
	c.Timing("test.metric.timing", 1)
}

func statsdMaxBufferLen(c statsd.Statser) {
	c.Gauge("internal.statsd.maxbuflen", statsd.Debug.MaxBufferLen)

}
