package mktorrent

import (
	"context"
	"crypto/sha1"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"blackbird/internal/rtorrent"
	"blackbird/internal/torrentfile"
)

func writeSized(t *testing.T, path string, size int, seed byte) {
	t.Helper()
	data := make([]byte, size)
	for i := range data {
		data[i] = seed + byte(i%251)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestValidate(t *testing.T) {
	good := Input{Source: "/dl/pack", Trackers: []string{"https://t.example/announce", "udp://u.example:1337/announce"}}
	if err := Validate(good); err != nil {
		t.Fatalf("good input rejected: %v", err)
	}
	cases := []Input{
		{Source: ""},
		{Source: "relative/path"},
		{Source: "/dl/pack", Name: "a/b"},
		{Source: "/dl/pack", Name: ".."},
		{Source: "/dl/pack", Name: strings.Repeat("x", 256)},
		{Source: "/dl/pack", Trackers: []string{"not a url"}},
		{Source: "/dl/pack", Trackers: []string{"ftp://h/x"}},
		{Source: "/dl/pack", Trackers: []string{"https://"}},
		{Source: "/dl/pack", PieceLength: 1000},
		{Source: "/dl/pack", PieceLength: 1 << 25},
		{Source: "/dl/pack", SourceTag: "has space"},
		{Source: "/dl/pack", SourceTag: strings.Repeat("x", 65)},
	}
	for i, in := range cases {
		if err := Validate(in); err == nil {
			t.Errorf("case %d (%+v) accepted", i, in)
		}
	}
	if err := Validate(Input{Source: "/dl/pack", PieceLength: 262144}); err != nil {
		t.Errorf("fixed 256KiB rejected: %v", err)
	}
}

func TestCollect(t *testing.T) {
	root := t.TempDir()
	writeSized(t, filepath.Join(root, "b.bin"), 100, 1)
	writeSized(t, filepath.Join(root, "a.bin"), 50, 2)
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSized(t, filepath.Join(sub, "c.bin"), 10, 3)

	files, multi, err := Collect(root)
	if err != nil {
		t.Fatal(err)
	}
	if !multi || len(files) != 3 {
		t.Fatalf("files = %+v multi = %v", files, multi)
	}
	// Lexicographic order defines the piece layout.
	if files[0].Rel[0] != "a.bin" || files[1].Rel[0] != "b.bin" || files[2].Rel[1] != "c.bin" {
		t.Fatalf("order = %+v", files)
	}
	if TotalBytes(files) != 160 {
		t.Fatalf("total = %d", TotalBytes(files))
	}

	single, multi, err := Collect(filepath.Join(root, "a.bin"))
	if err != nil || multi || len(single) != 1 || single[0].Size != 50 {
		t.Fatalf("single = %+v %v %v", single, multi, err)
	}

	link := filepath.Join(root, "escape")
	if err := os.Symlink(filepath.Join(root, "a.bin"), link); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Collect(root); err == nil {
		t.Fatal("symlink in source accepted")
	}
	if _, _, err := Collect(link); err == nil {
		t.Fatal("symlink source accepted")
	}
}

func TestAutoPieceLength(t *testing.T) {
	if got := AutoPieceLength(100); got != 65536 {
		t.Fatalf("tiny = %d", got)
	}
	if got := AutoPieceLength(2000 * 65536); got != 65536 {
		t.Fatalf("boundary = %d", got)
	}
	if got := AutoPieceLength(2000*65536 + 1); got != 131072 {
		t.Fatalf("over boundary = %d", got)
	}
	if got := AutoPieceLength(1 << 40); got != MaxPieceLength {
		t.Fatalf("huge = %d", got)
	}
}

// verifyPieces re-hashes the concatenated file stream independently and
// compares against the built pieces string.
func verifyPieces(t *testing.T, files []File, plen int64, pieces string) {
	t.Helper()
	var stream []byte
	for _, f := range files {
		data, err := os.ReadFile(f.Abs)
		if err != nil {
			t.Fatal(err)
		}
		stream = append(stream, data...)
	}
	var want []byte
	for off := int64(0); off < int64(len(stream)); off += plen {
		end := off + plen
		if end > int64(len(stream)) {
			end = int64(len(stream))
		}
		sum := sha1.Sum(stream[off:end])
		want = append(want, sum[:]...)
	}
	if string(want) != pieces {
		t.Fatal("piece hashes do not match an independent re-hash")
	}
}

func TestBuildMultiRoundTrip(t *testing.T) {
	root := t.TempDir()
	writeSized(t, filepath.Join(root, "b.bin"), 300000, 7)
	writeSized(t, filepath.Join(root, "a.bin"), 100000, 9)
	if err := os.WriteFile(filepath.Join(root, "empty.bin"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	files, multi, err := Collect(root)
	if err != nil {
		t.Fatal(err)
	}
	in := Input{
		Source: root, Name: "pack", PieceLength: 131072,
		Trackers:  []string{"https://primary.example/a", "udp://second.example:1337/a"},
		Private:   true,
		Comment:   "test pack",
		SourceTag: "TEST",
	}
	var progSeen bool
	res, err := Build(context.Background(), in, files, multi, func(p Progress) { progSeen = true })
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalBytes != 400000 || res.FileCount != 3 || res.PieceLength != 131072 || res.PieceCount != 4 {
		t.Fatalf("result = %+v", res)
	}
	if !progSeen {
		t.Fatal("no progress reported")
	}
	meta, err := torrentfile.Parse(res.Data)
	if err != nil {
		t.Fatalf("built torrent does not parse: %v", err)
	}
	if meta.Infohash != res.Infohash || meta.Name != "pack" || !meta.Private ||
		meta.Comment != "test pack" || meta.CreatedBy != "Blackbird" {
		t.Fatalf("meta = %+v", meta)
	}
	// Structural check on the raw dict.
	raw, _, err := decodeTop(res.Data)
	if err != nil {
		t.Fatal(err)
	}
	if raw["announce"] != "https://primary.example/a" {
		t.Fatalf("announce = %v", raw["announce"])
	}
	tiers, ok := raw["announce-list"].([]any)
	if !ok || len(tiers) != 1 {
		t.Fatalf("announce-list = %v", raw["announce-list"])
	}
	info := raw["info"].(map[string]any)
	if info["source"] != "TEST" || info["private"] != int64(1) {
		t.Fatalf("info extras = %v", info)
	}
	fileEntries, ok := info["files"].([]any)
	if !ok || len(fileEntries) != 3 {
		t.Fatalf("files = %v", info["files"])
	}
	// Empty files occupy entries but contribute no piece bytes.
	if fileEntries[2].(map[string]any)["length"] != int64(0) {
		t.Fatalf("empty entry = %v", fileEntries[2])
	}
	verifyPieces(t, files, 131072, info["pieces"].(string))
}

func TestBuildSingleFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "movie.mkv")
	writeSized(t, path, 50000, 4)
	files, multi, err := Collect(path)
	if err != nil {
		t.Fatal(err)
	}
	if multi {
		t.Fatal("single file reported as multi")
	}
	res, err := Build(context.Background(), Input{Source: path}, files, multi, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Name != "movie.mkv" || res.PieceLength != 65536 || res.PieceCount != 1 {
		t.Fatalf("result = %+v", res)
	}
	raw, _, err := decodeTop(res.Data)
	if err != nil {
		t.Fatal(err)
	}
	info := raw["info"].(map[string]any)
	if info["length"] != int64(50000) {
		t.Fatalf("length = %v", info["length"])
	}
	if _, ok := raw["announce"]; ok {
		t.Fatalf("announce present without trackers: %v", raw)
	}
	verifyPieces(t, files, 65536, info["pieces"].(string))
}

func TestBuildRefusals(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	files, multi, err := Collect(filepath.Join(root, "empty"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Build(context.Background(), Input{Source: filepath.Join(root, "empty")}, files, multi, nil); err == nil {
		t.Fatal("empty source accepted")
	}
	// Cancellation aborts mid-hash.
	big := filepath.Join(root, "big.bin")
	writeSized(t, big, 4<<20, 5)
	files, multi, err = Collect(big)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Build(ctx, Input{Source: big}, files, multi, nil); err == nil {
		t.Fatal("cancelled build succeeded")
	}
}

// decodeTop is a test-local bencode reader producing int64/string trees.
func decodeTop(data []byte) (map[string]any, []byte, error) {
	v, rest, err := decodeAny(data)
	if err != nil {
		return nil, nil, err
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("root is not a dict")
	}
	return m, rest, nil
}

func decodeAny(data []byte) (any, []byte, error) {
	if len(data) == 0 {
		return nil, nil, fmt.Errorf("end of data")
	}
	switch c := data[0]; {
	case c == 'i':
		end := strings.IndexByte(string(data), 'e')
		if end < 0 {
			return nil, nil, fmt.Errorf("unterminated int")
		}
		var n int64
		for _, d := range data[1:end] {
			if d < '0' || d > '9' {
				return nil, nil, fmt.Errorf("bad int")
			}
			n = n*10 + int64(d-'0')
		}
		return n, data[end+1:], nil
	case c >= '0' && c <= '9':
		colon := strings.IndexByte(string(data), ':')
		if colon < 0 {
			return nil, nil, fmt.Errorf("bad string")
		}
		var n int
		for _, d := range data[:colon] {
			n = n*10 + int(d-'0')
		}
		if len(data) < colon+1+n {
			return nil, nil, fmt.Errorf("short string")
		}
		return string(data[colon+1 : colon+1+n]), data[colon+1+n:], nil
	case c == 'l':
		var out []any
		rest := data[1:]
		for {
			if len(rest) == 0 {
				return nil, nil, fmt.Errorf("unterminated list")
			}
			if rest[0] == 'e' {
				return out, rest[1:], nil
			}
			item, after, err := decodeAny(rest)
			if err != nil {
				return nil, nil, err
			}
			out = append(out, item)
			rest = after
		}
	case c == 'd':
		out := map[string]any{}
		rest := data[1:]
		for {
			if len(rest) == 0 {
				return nil, nil, fmt.Errorf("unterminated dict")
			}
			if rest[0] == 'e' {
				return out, rest[1:], nil
			}
			key, after, err := decodeAny(rest)
			if err != nil {
				return nil, nil, err
			}
			val, after2, err := decodeAny(after)
			if err != nil {
				return nil, nil, err
			}
			out[key.(string)] = val
			rest = after2
		}
	default:
		return nil, nil, fmt.Errorf("bad token %q", c)
	}
}

type stubDaemon struct {
	adds []rtorrent.AddOptions
	data [][]byte
	fail error
}

func (d *stubDaemon) AddTorrentFile(_ context.Context, data []byte, opts rtorrent.AddOptions) error {
	if d.fail != nil {
		return d.fail
	}
	d.adds = append(d.adds, opts)
	d.data = append(d.data, append([]byte(nil), data...))
	return nil
}

func testRoots(root string) func() []string { return func() []string { return []string{root} } }

func waitJob(t *testing.T, s *Service, id string) Job {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		job, ok := s.Status(id)
		if !ok {
			t.Fatal("job vanished")
		}
		if job.Status != "running" {
			return job
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("job never finished")
	return Job{}
}

func TestServiceSubmitAndDownload(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "pack")
	if err := os.Mkdir(src, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSized(t, filepath.Join(src, "f.bin"), 200000, 3)
	daemon := &stubDaemon{}
	svc := New(Options{Roots: testRoots(root), Daemon: daemon})

	job, err := svc.Submit(Spec{Input: Input{Source: src, Trackers: []string{"https://t.example/a"}}}, "tester")
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != "running" || job.TotalBytes != 200000 || job.FileCount != 1 {
		t.Fatalf("submitted = %+v", job)
	}
	done := waitJob(t, svc, job.ID)
	if done.Status != "completed" || done.Infohash == "" || done.TorrentSize == 0 {
		t.Fatalf("done = %+v", done)
	}
	if done.PieceCount != 4 || done.PiecesDone != 4 || done.BytesHashed != 200000 {
		t.Fatalf("progress = %+v", done)
	}
	data, name, ok := svc.Data(job.ID)
	if !ok || name != "pack" {
		t.Fatalf("data ok = %v name = %q", ok, name)
	}
	if meta, err := torrentfile.Parse(data); err != nil || meta.Infohash != done.Infohash {
		t.Fatalf("downloaded bytes invalid: %v %+v", err, meta)
	}
	if len(daemon.adds) != 0 {
		t.Fatalf("unexpected session add: %+v", daemon.adds)
	}
}

func TestServiceAddToSession(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "pack")
	if err := os.Mkdir(src, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSized(t, filepath.Join(src, "f.bin"), 1000, 3)
	daemon := &stubDaemon{}
	svc := New(Options{Roots: testRoots(root), Daemon: daemon})

	job, err := svc.Submit(Spec{
		Input:        Input{Source: src},
		AddToSession: true, Start: true, Label: "iso",
	}, "tester")
	if err != nil {
		t.Fatal(err)
	}
	done := waitJob(t, svc, job.ID)
	if done.Status != "completed" || !done.Added || done.AddedHash != done.Infohash || done.AddError != "" {
		t.Fatalf("done = %+v", done)
	}
	if len(daemon.adds) != 1 {
		t.Fatalf("adds = %+v", daemon.adds)
	}
	opts := daemon.adds[0]
	if !opts.Start {
		t.Fatalf("add not started: %+v", opts)
	}
	joined := strings.Join(opts.ExtraCommands, " ")
	// The session directory is the source's parent, so the daemon finds the
	// packaged directory/file where it already sits. Compare symlink-aware:
	// macOS temp dirs resolve /var to /private/var.
	resolvedSrc, err := filepath.EvalSymlinks(src)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(joined, "d.directory.set="+filepath.Dir(resolvedSrc)) || !strings.Contains(joined, "d.custom1.set=iso") {
		t.Fatalf("directory tie/label missing: %+v", opts)
	}
}

func TestServiceValidationAndEviction(t *testing.T) {
	root := t.TempDir()
	svc := New(Options{Roots: testRoots(root), Retain: 2})

	if _, err := svc.Submit(Spec{Input: Input{Source: "/elsewhere/pack"}}, "t"); err == nil {
		t.Fatal("outside-roots source accepted")
	}
	missing := filepath.Join(root, "nope")
	if _, err := svc.Submit(Spec{Input: Input{Source: missing}}, "t"); err == nil {
		t.Fatal("missing source accepted")
	}
	empty := filepath.Join(root, "empty")
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Submit(Spec{Input: Input{Source: empty}}, "t"); err == nil {
		t.Fatal("empty source accepted")
	}
	if _, ok := svc.Status("create-999"); ok {
		t.Fatal("unknown job found")
	}
	if _, ok := svc.Cancel("create-999"); ok {
		t.Fatal("unknown job cancelled")
	}
	if _, _, ok := svc.Data("create-999"); ok {
		t.Fatal("unknown job data served")
	}

	src := filepath.Join(root, "pack")
	if err := os.Mkdir(src, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSized(t, filepath.Join(src, "f.bin"), 100, 1)
	// Submit and wait one at a time: each finish evicts past Retain=2, so
	// no job vanishes while the test is still waiting on it.
	var ids []string
	for i := 0; i < 4; i++ {
		job, err := svc.Submit(Spec{Input: Input{Source: src}}, "t")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, job.ID)
		waitJob(t, svc, job.ID)
	}
	// Retain=2: only the two newest terminal jobs survive.
	for _, id := range ids[:2] {
		if _, ok := svc.Status(id); ok {
			t.Fatalf("evicted job %s still present", id)
		}
	}
	if _, ok := svc.Status(ids[3]); !ok {
		t.Fatalf("newest job %s missing", ids[3])
	}
}
