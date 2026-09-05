//go:build linux

package host

import (
	"os"
	"strconv"
	"strings"
)

// readLinuxLoad parses /proc/loadavg (first three fields).
func readLinuxLoad(st *Stats) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return
	}
	loads, ok := parseLoadavg(string(data))
	if !ok {
		return
	}
	st.Load1, st.Load5, st.Load15, st.LoadOK = loads[0], loads[1], loads[2], true
}

// readLinuxMem parses /proc/meminfo (MemTotal, MemAvailable with MemFree
// fallback) into bytes.
func readLinuxMem(st *Stats) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return
	}
	total, avail, ok := parseMeminfo(string(data))
	if !ok {
		return
	}
	st.MemTotal, st.MemAvail, st.MemOK = total, avail, true
}

// readLinuxSelf reports process RSS from /proc/self/status (VmRSS).
func readLinuxSelf(st *Stats) {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, found := strings.Cut(line, ":")
		if !found || strings.TrimSpace(key) != "VmRSS" {
			continue
		}
		fields := strings.Fields(value)
		if len(fields) < 1 {
			continue
		}
		if kb, err := strconv.ParseUint(fields[0], 10, 64); err == nil && kb > 0 {
			st.SelfBytes, st.SelfOK = kb*1024, true
		}
		return
	}
}

// platformSnapshot fills Linux host stats.
func platformSnapshot(st *Stats) {
	readLinuxLoad(st)
	readLinuxMem(st)
	readLinuxSelf(st)
}
