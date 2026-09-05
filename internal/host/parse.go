package host

import (
	"encoding/binary"
	"strconv"
	"strings"
)

// This file holds the pure parsing helpers shared across platforms so the
// unit tests build on every GOOS; only the readers that touch /proc or
// sysctl stay behind build tags.

// parseLoadavg parses "1.23 0.45 0.67 ..." into three floats.
func parseLoadavg(s string) ([3]float64, bool) {
	var out [3]float64
	fields := strings.Fields(s)
	if len(fields) < 3 {
		return out, false
	}
	for i := 0; i < 3; i++ {
		v, err := strconv.ParseFloat(fields[i], 64)
		if err != nil || v < 0 {
			return out, false
		}
		out[i] = v
	}
	return out, true
}

// parseMeminfo extracts MemTotal and MemAvailable (kB) from meminfo text. It
// falls back to MemFree when MemAvailable is absent (old kernels).
func parseMeminfo(s string) (total, avail uint64, ok bool) {
	var memFree, buffers, cached uint64
	var hasFree bool
	for _, line := range strings.Split(s, "\n") {
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		fields := strings.Fields(value)
		if len(fields) < 1 {
			continue
		}
		kb, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}
		switch strings.TrimSpace(key) {
		case "MemTotal":
			total = kb * 1024
		case "MemAvailable":
			avail = kb * 1024
		case "MemFree":
			memFree, hasFree = kb*1024, true
		case "Buffers":
			buffers = kb * 1024
		case "Cached":
			cached = kb * 1024
		}
	}
	if total == 0 {
		return 0, 0, false
	}
	if avail == 0 && hasFree {
		avail = memFree + buffers + cached
	}
	return total, avail, true
}

// darwinLoadScale is the FSHIFT=11 fixed-point scale of vm.loadavg.
const darwinLoadScale = 2048

func padded(b []byte, n int) []byte {
	if len(b) >= n {
		return b[:n]
	}
	out := make([]byte, n)
	copy(out, b)
	return out
}

// parseDarwinLoadavg decodes vm.loadavg bytes into three load averages.
func parseDarwinLoadavg(b []byte) ([3]float64, bool) {
	var out [3]float64
	if len(b) < 12 {
		return out, false
	}
	for i := 0; i < 3; i++ {
		raw := int32(binary.LittleEndian.Uint32(b[4*i : 4*i+4]))
		if raw < 0 {
			return out, false
		}
		out[i] = float64(raw) / darwinLoadScale
	}
	return out, true
}

func parseDarwinUint64(b []byte) (uint64, bool) {
	if len(b) == 0 || len(b) > 8 {
		return 0, false
	}
	return binary.LittleEndian.Uint64(padded(b, 8)), true
}

func parseDarwinUint32(b []byte) (uint64, bool) {
	if len(b) == 0 || len(b) > 4 {
		return 0, false
	}
	return uint64(binary.LittleEndian.Uint32(padded(b, 4))), true
}
