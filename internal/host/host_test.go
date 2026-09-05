package host

import (
	"runtime"
	"testing"
)

const meminfoFixture = `MemTotal:       16384000 kB
MemFree:          2048000 kB
MemAvailable:    12288000 kB
Buffers:           512000 kB
Cached:           4096000 kB
SwapTotal:        2097152 kB
`

func TestParseMeminfo(t *testing.T) {
	total, avail, ok := parseMeminfo(meminfoFixture)
	if !ok {
		t.Fatal("parse failed")
	}
	if total != 16384000*1024 || avail != 12288000*1024 {
		t.Fatalf("total=%d avail=%d", total, avail)
	}
}

func TestParseMeminfoFreeFallback(t *testing.T) {
	// Old kernels lack MemAvailable: Free+Buffers+Cached applies.
	total, avail, ok := parseMeminfo("MemTotal:       8000000 kB\nMemFree:         1000000 kB\nBuffers:          200000 kB\nCached:           800000 kB\n")
	if !ok {
		t.Fatal("parse failed")
	}
	if total != 8000000*1024 || avail != 2000000*1024 {
		t.Fatalf("total=%d avail=%d", total, avail)
	}
	if _, _, ok := parseMeminfo("MemFree: 100 kB\n"); ok {
		t.Fatal("missing total must fail")
	}
}

func TestParseLoadavg(t *testing.T) {
	loads, ok := parseLoadavg("2.50 1.25 0.75 3/512 12345\n")
	if !ok {
		t.Fatal("parse failed")
	}
	if loads != [3]float64{2.5, 1.25, 0.75} {
		t.Fatalf("loads = %v", loads)
	}
	if _, ok := parseLoadavg("bogus\n"); ok {
		t.Fatal("garbage must fail")
	}
	if _, ok := parseLoadavg("1.0 2.0\n"); ok {
		t.Fatal("short line must fail")
	}
}

func TestParseDarwinLoadavg(t *testing.T) {
	// Synthetic fixed-point triple at FSHIFT=11, verified live against
	// sysctl -n vm.loadavg (21.37/22.77/25.31 on both paths).
	raw := []byte{
		0x43, 0xC0, 0x00, 0x00,
		0xBF, 0xBA, 0x00, 0x00,
		0x64, 0xCC, 0x00, 0x00,
	}
	loads, ok := parseDarwinLoadavg(raw)
	if !ok {
		t.Fatal("parse failed")
	}
	want := [3]float64{49219.0 / 2048, 47807.0 / 2048, 52324.0 / 2048}
	for i := range want {
		if loads[i] != want[i] {
			t.Fatalf("loads = %v, want %v", loads, want)
		}
	}
	if _, ok := parseDarwinLoadavg([]byte{1, 2, 3}); ok {
		t.Fatal("short input must fail")
	}
}

func TestParseDarwinUint(t *testing.T) {
	// Zero-trimmed little-endian (Go's syscall.Sysctl trims trailing NULs).
	if v, ok := parseDarwinUint64([]byte{0x00, 0x00, 0x00, 0x00, 0x10, 0x00, 0x00}); !ok || v != 0x1000000000 {
		t.Fatalf("u64 = %d, %v", v, ok)
	}
	if v, ok := parseDarwinUint32([]byte{0xC8, 0x0F, 0x00}); !ok || v != 0x0FC8 {
		t.Fatalf("u32 = %d, %v", v, ok)
	}
	if _, ok := parseDarwinUint64(nil); ok {
		t.Fatal("empty must fail")
	}
	if _, ok := parseDarwinUint64(make([]byte, 9)); ok {
		t.Fatal("oversize must fail")
	}
}

// TestSnapshotSmoke asserts best-effort collection never crashes and always
// reports heap bytes; OS fields are validated only where readable.
func TestSnapshotSmoke(t *testing.T) {
	st := Snapshot()
	if st.HeapBytes == 0 {
		t.Fatal("heap bytes missing")
	}
	t.Logf("goos=%s loadOK=%v memOK=%v selfOK=%v heap=%d", runtime.GOOS, st.LoadOK, st.MemOK, st.SelfOK, st.HeapBytes)
	switch runtime.GOOS {
	case "linux", "darwin":
		if !st.LoadOK {
			t.Error("load average should be readable")
		}
		if !st.MemOK || st.MemTotal == 0 {
			t.Error("memory total should be readable")
		}
	}
	if st.LoadOK && (st.Load1 < 0 || st.Load5 < 0 || st.Load15 < 0) {
		t.Fatalf("negative loads: %+v", st)
	}
}
