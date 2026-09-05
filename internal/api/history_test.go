package api

import (
	"os"
	"path/filepath"
	"testing"

	"blackbird/internal/torrentfile"
)

// Bencode fixture with a comment + created-by. Length prefixes must match the
// byte count of each value exactly.
const commentedTorrent = "d" +
	"7:comment" + "14:Backfilled now" +
	"10:created by" + "11:OldTool 1.0" +
	"13:creation date" + "i1756700300e" +
	"4:info" + "d" + "4:name" + "1:x" + "6:length" + "i1e" + "e" +
	"e"

func TestMetaStoreSessionBackfill(t *testing.T) {
	dir := t.TempDir()
	// Name the session file by the torrent's actual infohash, the way
	// rTorrent writes <hash>.torrent into the session directory.
	parsed, err := torrentfile.Parse([]byte(commentedTorrent))
	if err != nil {
		t.Fatal(err)
	}
	hash := parsed.Infohash
	if err := os.WriteFile(filepath.Join(dir, hash+".torrent"), []byte(commentedTorrent), 0o600); err != nil {
		t.Fatal(err)
	}
	ms := newMetaStore(func() string { return dir })

	meta, ok := ms.get(hash)
	if !ok {
		t.Fatal("session backfill did not find metadata")
	}
	if meta.Comment != "Backfilled now" || meta.CreatedBy != "OldTool 1.0" {
		t.Fatalf("meta = %+v", meta)
	}
	if meta.CreatedAt == nil {
		t.Fatal("creationDate should be parsed")
	}

	// A second read hits the cache (no repeated probe) — cannot observe the
	// miss flag directly, but the value stays consistent.
	again, ok := ms.get(hash)
	if !ok || again.Comment != "Backfilled now" {
		t.Fatalf("cached read = %+v %v", again, ok)
	}

	// Unknown hashes are remembered as misses and resolve to not-found.
	if _, ok := ms.get("0000000000000000000000000000000000000000"); ok {
		t.Fatal("unknown hash should not resolve")
	}
}

func TestMetaStoreSessionDisabled(t *testing.T) {
	ms := newMetaStore(nil)
	hash := "abcdefabcdefabcdefabcdefabcdefabcdefabcd"
	// No session dir configured: put/get still work from the add-time path.
	ms.put(hash, torrentMeta{Comment: "Added live"})
	meta, ok := ms.get(hash)
	if !ok || meta.Comment != "Added live" {
		t.Fatalf("put/get = %+v %v", meta, ok)
	}
	// An uncached hash returns not-found without probing a directory.
	if _, ok := ms.get("1111111111111111111111111111111111111111"); ok {
		t.Fatal("uncached hash should not resolve without a session dir")
	}
}

func TestMetaStorePrefersAddTimeCapture(t *testing.T) {
	dir := t.TempDir()
	hash := "abcdefabcdefabcdefabcdefabcdefabcdefabcd"
	// Session file has one comment; add-time capture has another. The cache
	// entry from put() must win.
	if err := os.WriteFile(filepath.Join(dir, hash+".torrent"), []byte(commentedTorrent), 0o600); err != nil {
		t.Fatal(err)
	}
	ms := newMetaStore(func() string { return dir })
	ms.put(hash, torrentMeta{Comment: "Added via Blackbird"})

	meta, ok := ms.get(hash)
	if !ok || meta.Comment != "Added via Blackbird" {
		t.Fatalf("add-time capture should win: %+v %v", meta, ok)
	}
}
