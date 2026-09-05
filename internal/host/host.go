// Package host implements PAR-5.2 host cards: load average and memory from
// native per-OS sources, with no new dependencies. Linux reads /proc
// (production); darwin uses sysctl (dev machines). Anything unavailable is
// omitted (OK=false) and the UI renders a dash — telemetry must never break
// the console.
package host

import (
	"runtime"
)

// Stats is one host snapshot. OK flags mark which groups were readable.
type Stats struct {
	Load1, Load5, Load15 float64
	LoadOK               bool
	MemTotal, MemAvail   uint64
	MemOK                bool // total known; avail may still be 0 (unknown)
	SelfBytes            uint64
	SelfOK               bool
	HeapBytes            uint64 // Go heap live bytes (always available)
}

// Snapshot gathers best-effort host stats plus the process's own memory.
func Snapshot() Stats {
	var st Stats
	platformSnapshot(&st)
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	st.HeapBytes = mem.HeapAlloc
	return st
}
