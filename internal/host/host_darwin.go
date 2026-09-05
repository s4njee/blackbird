//go:build darwin

package host

import (
	"syscall"
)

// Darwin sources (verified live on arm64): vm.loadavg carries three
// FSHIFT=11 fixed-point loads in its first 12 bytes (Go trims trailing NULs,
// so only the length is checked); hw.memsize/hw.pagesize decode as
// zero-trimmed little-endian uint64; vm.page_free_count as zero-trimmed
// little-endian uint32. Memory available counts free pages only (inactive /
// speculative excluded) — a conservative lower bound, documented here.

// readDarwinLoad reads vm.loadavg.
func readDarwinLoad(st *Stats) {
	b, err := syscall.Sysctl("vm.loadavg")
	if err != nil {
		return
	}
	loads, ok := parseDarwinLoadavg([]byte(b))
	if !ok {
		return
	}
	st.Load1, st.Load5, st.Load15, st.LoadOK = loads[0], loads[1], loads[2], true
}

// readDarwinMem reads hw.memsize (total) and free pages (available lower
// bound). Process RSS has no stable MIB without Mach traps, so SelfOK stays
// false and the UI shows heap bytes instead.
func readDarwinMem(st *Stats) {
	total, err := syscall.Sysctl("hw.memsize")
	if err != nil {
		return
	}
	memTotal, ok := parseDarwinUint64([]byte(total))
	if !ok || memTotal == 0 {
		return
	}
	st.MemTotal, st.MemOK = memTotal, true
	free, err := syscall.Sysctl("vm.page_free_count")
	if err != nil {
		return
	}
	pages, ok := parseDarwinUint32([]byte(free))
	if !ok {
		return
	}
	pageSize, err := syscall.Sysctl("hw.pagesize")
	if err != nil {
		return
	}
	size, ok := parseDarwinUint64([]byte(pageSize))
	if !ok || size == 0 {
		return
	}
	st.MemAvail = pages * size
}

// platformSnapshot fills Darwin host stats.
func platformSnapshot(st *Stats) {
	readDarwinLoad(st)
	readDarwinMem(st)
}
