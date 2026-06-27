package metrics

import (
	"os"
	"regexp"
	"strconv"
	"strings"
)

type Reader = func(string) string

func readFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

func ReadFileFn() Reader { return readFile }

var cpuUsageRe = regexp.MustCompile(`usage_usec\s+(\d+)`)

func ReadCgroupCPU(read Reader) uint64 {
	m := cpuUsageRe.FindStringSubmatch(read("/sys/fs/cgroup/cpu.stat"))
	if len(m) < 2 {
		return 0
	}
	n, _ := strconv.ParseUint(m[1], 10, 64)
	return n
}

func ReadCgroupMemory(read Reader) (current, max uint64, maxSet bool) {
	c, _ := strconv.ParseUint(strings.TrimSpace(read("/sys/fs/cgroup/memory.current")), 10, 64)
	mr := strings.TrimSpace(read("/sys/fs/cgroup/memory.max"))
	if mr == "max" || mr == "" {
		return c, 0, false
	}
	mx, _ := strconv.ParseUint(mr, 10, 64)
	return c, mx, true
}

func ReadNetDev(read Reader) (rx, tx uint64) {
	text := read("/proc/net/dev")
	lines := strings.Split(text, "\n")
	if len(lines) < 3 {
		return 0, 0
	}
	for _, line := range lines[2:] {
		parts := strings.SplitN(strings.TrimSpace(line), ":", 2)
		if len(parts) != 2 {
			continue
		}
		iface := strings.TrimSpace(parts[0])
		if iface == "lo" {
			continue
		}
		nums := strings.Fields(parts[1])
		if len(nums) < 16 {
			continue
		}
		r, _ := strconv.ParseUint(nums[0], 10, 64)
		t, _ := strconv.ParseUint(nums[8], 10, 64)
		rx += r
		tx += t
	}
	return rx, tx
}
