package api

import (
	"bytes"
	"compress/flate"
	"encoding/json"
	"testing"
	"time"

	"blackbird/internal/poller"
	"blackbird/internal/rtorrent"
)

// syntheticSession builds a deterministic large session: fixed timestamps so
// sizes are stable across runs (a stand-in for the PERF-6.6 fixture, which
// does not exist yet).
func syntheticSession(n int) []rtorrent.Torrent {
	base := time.Unix(1756700000, 0)
	out := make([]rtorrent.Torrent, n)
	for i := 0; i < n; i++ {
		out[i] = rtorrent.Torrent{
			Hash:            "hash-" + itoa6(i),
			Name:            "torrent-number-with-a-plausibly-long-name-for-size",
			SizeBytes:       6474842112,
			CompletedBytes:  3974842112,
			LeftBytes:       2500000000,
			DownloadedBytes: 3974842112,
			UploadedBytes:   9582132740,
			Percent:         61.4,
			Complete:        false,
			IsOpen:          true,
			State:           rtorrent.StateDownloading,
			Seeds:           38,
			Peers:           112,
			DownRate:        412000,
			UpRate:          128000,
			EtaSeconds:      6072.0,
			Ratio:           2.41,
			Label:           "iso",
			Throttle:        "bulk",
			Directory:       "/mnt/data/iso",
			Connection:      "connected",
			AddedAt:         base,
			TrackerHost:     "torrent.ubuntu.com",
			TrackerStatus:   "Working",
			BasePath:        "/mnt/data/iso/torrent-number-with-a-plausibly-long-name-for-size",
			Priority:        2,
		}
	}
	return out
}

func itoa6(i int) string {
	s := "000000"
	n := ""
	for v := i; v > 0; v /= 10 {
		n = string(rune('0'+v%10)) + n
	}
	if n == "" {
		n = "0"
	}
	return s[:len(s)-len(n)] + n
}

// steadyTick mutates 200 rows' live counters the way a busy 2s poll does.
func steadyTick(rows []rtorrent.Torrent) (v1 []rtorrent.Torrent, patches []poller.TorrentPatch) {
	return steadyTickN(rows, 200)
}

// steadyTickN is steadyTick generalized to n live rows for the per-fixture
// size table.
func steadyTickN(rows []rtorrent.Torrent, n int) (v1 []rtorrent.Torrent, patches []poller.TorrentPatch) {
	for i := 0; i < n; i++ {
		t := rows[i]
		t.DownRate += int64(i)
		t.UpRate += int64(i)
		t.CompletedBytes += 412000
		t.DownloadedBytes += 412000
		t.UploadedBytes += 128000
		t.LeftBytes -= 412000
		t.Percent += 0.01
		t.Ratio += 0.001
		v1 = append(v1, t)
		patches = append(patches, poller.TorrentPatch{Hash: t.Hash, Fields: map[string]any{
			"downRate": t.DownRate, "upRate": t.UpRate,
			"completedBytes": t.CompletedBytes, "downloadedBytes": t.DownloadedBytes,
			"uploadedBytes": t.UploadedBytes, "leftBytes": t.LeftBytes,
			"percent": t.Percent, "ratio": t.Ratio,
		}})
	}
	return v1, patches
}

func flateSize(t *testing.T, data []byte) int {
	t.Helper()
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.DefaultCompression)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Len()
}

// TestDeltaV2SteadyStateSizes measures the PERF-6.2 payload win on a
// synthetic 5,000-torrent session with 200 rows changing per tick (the Epic 6
// target shape), plus the permessage-deflate proxy (raw flate, the same
// codec gorilla negotiates). Numbers are logged for the PERF-6.6 report.
func TestDeltaV2SteadyStateSizes(t *testing.T) {
	rows := syntheticSession(5000)
	changed, patches := steadyTick(rows)
	at := time.Unix(1756700100, 0)

	v1 := v1Delta{Changed: changed, GlobalChanged: true,
		Global:     &rtorrent.GlobalStats{DownRate: 41200000, UpRate: 12800000},
		Aggregates: &poller.Aggregates{Status: map[rtorrent.State]int{"all": 5000}},
		At:         at}
	v2 := v2Delta{ChangedPatches: patches, GlobalChanged: true,
		Global:          &rtorrent.GlobalStats{DownRate: 41200000, UpRate: 12800000},
		AggregatesPatch: &poller.AggregatesPatch{Status: map[rtorrent.State]int{"all": 5000}},
		At:              at}

	v1Bytes, err := json.Marshal(wsEnvelope{V: 1, Type: "delta", Data: v1})
	if err != nil {
		t.Fatal(err)
	}
	v2Bytes, err := json.Marshal(wsEnvelope{V: 2, Type: "delta", Data: v2})
	if err != nil {
		t.Fatal(err)
	}
	v1z, v2z := flateSize(t, v1Bytes), flateSize(t, v2Bytes)
	t.Logf("steady tick: v1=%d v2=%d (%.1f%%) v1+deflate=%d v2+deflate=%d (%.1f%% of v1)",
		len(v1Bytes), len(v2Bytes), 100*float64(len(v2Bytes))/float64(len(v1Bytes)),
		v1z, v2z, 100*float64(v2z)/float64(len(v1Bytes)))

	if len(v2Bytes)*2 > len(v1Bytes) {
		t.Fatalf("v2 (%d) is not substantially smaller than v1 (%d)", len(v2Bytes), len(v1Bytes))
	}
	if v2z >= len(v2Bytes) {
		t.Fatalf("deflate did not shrink v2 (%d -> %d)", len(v2Bytes), v2z)
	}
}

// BenchmarkDeltaEncoding measures v1 vs v2 wire marshaling for a 200-change
// tick so the report and the regression guard cover encoding cost.
func BenchmarkDeltaEncoding(b *testing.B) {
	rows := syntheticSession(5000)
	changed, patches := steadyTick(rows)
	at := time.Unix(1756700100, 0)
	globals := &rtorrent.GlobalStats{DownRate: 41200000, UpRate: 12800000}
	v1 := v1Delta{Changed: changed, GlobalChanged: true, Global: globals,
		Aggregates: &poller.Aggregates{Status: map[rtorrent.State]int{"all": 5000}}, At: at}
	v2 := v2Delta{ChangedPatches: patches, GlobalChanged: true, Global: globals,
		AggregatesPatch: &poller.AggregatesPatch{Status: map[rtorrent.State]int{"all": 5000}}, At: at}
	b.Run("v1", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := json.Marshal(wsEnvelope{V: 1, Type: "delta", Data: v1}); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("v2", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := json.Marshal(wsEnvelope{V: 2, Type: "delta", Data: v2}); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// TestPerfDeltaSizes logs the v1/v2 wire table per fixture size for the
// performance report (PERF-6.6). Report-only: numbers go to the test log
// for pasting into docs/performance.md.
func TestPerfDeltaSizes(t *testing.T) {
	for _, fixture := range []struct {
		rows, live int
	}{{500, 20}, {5000, 200}, {20000, 800}} {
		rows := syntheticSession(fixture.rows)
		changed, patches := steadyTickN(rows, fixture.live)
		at := time.Unix(1756700100, 0)
		globals := &rtorrent.GlobalStats{DownRate: 41200000, UpRate: 12800000}
		aggs := &poller.Aggregates{Status: map[rtorrent.State]int{"all": fixture.rows}}
		v1, err := json.Marshal(wsEnvelope{V: 1, Type: "delta", Data: v1Delta{
			Changed: changed, GlobalChanged: true, Global: globals, Aggregates: aggs, At: at}})
		if err != nil {
			t.Fatal(err)
		}
		v2, err := json.Marshal(wsEnvelope{V: 2, Type: "delta", Data: v2Delta{
			ChangedPatches: patches, GlobalChanged: true, Global: globals,
			AggregatesPatch: &poller.AggregatesPatch{Status: map[rtorrent.State]int{"all": fixture.rows}}, At: at}})
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("delta/%d: v1=%d v2=%d (%.1f%%) v1+deflate=%d v2+deflate=%d",
			fixture.rows, len(v1), len(v2), 100*float64(len(v2))/float64(len(v1)),
			flateSize(t, v1), flateSize(t, v2))
	}
}
