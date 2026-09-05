package rss

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestParseRSSFixture(t *testing.T) {
	items, err := ParseFeed(loadFixture(t, "tv-rss.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("items = %d, want 3", len(items))
	}
	first := items[0]
	if first.Title != "Show.Name.S02E04.1080p.WEB.h264-EXAMPLE" {
		t.Fatalf("title = %q", first.Title)
	}
	if first.ID != "https://tracker.example/torrents/918273" {
		t.Fatalf("id = %q", first.ID)
	}
	if first.Enclosure.URL != "https://tracker.example/download/918273/show.name.s02e04.torrent" ||
		first.Enclosure.Length != 73400320 || first.Enclosure.Type != "application/x-bittorrent" {
		t.Fatalf("enclosure = %+v", first.Enclosure)
	}
	if len(first.Categories) != 2 || first.Categories[0] != "TV" || first.Categories[1] != "HD" {
		t.Fatalf("categories = %+v", first.Categories)
	}
	wantDate := time.Date(2026, 9, 2, 14, 12, 0, 0, time.UTC)
	if !first.Published.Equal(wantDate) {
		t.Fatalf("published = %v, want %v", first.Published, wantDate)
	}

	// Zero-length enclosure parses as unknown (-1), keyed by guid.
	second := items[1]
	if second.Enclosure.Length != -1 {
		t.Fatalf("zero length enclosure = %d, want -1", second.Enclosure.Length)
	}
	if second.ID != "918200" {
		t.Fatalf("id = %q", second.ID)
	}

	// Magnet enclosure with no guid falls back to the enclosure URL.
	third := items[2]
	if third.Enclosure.URL == "" || third.Enclosure.URL[:7] != "magnet:" {
		t.Fatalf("enclosure = %+v", third.Enclosure)
	}
	if third.ID != third.Enclosure.URL {
		t.Fatalf("id = %q, want enclosure fallback", third.ID)
	}
}

func TestParseAtomFixture(t *testing.T) {
	items, err := ParseFeed(loadFixture(t, "music-atom.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("items = %d, want 3", len(items))
	}
	first := items[0]
	if first.Title != "Artist - Album (2026) [FLAC]" || first.ID != "tag:music.example,2026:555" {
		t.Fatalf("first = %+v", first)
	}
	if first.Enclosure.URL != "https://music.example/dl/555.torrent" || first.Enclosure.Length != 412876800 {
		t.Fatalf("enclosure = %+v", first.Enclosure)
	}
	if len(first.Categories) != 2 || first.Categories[0] != "Music" || first.Categories[1] != "FLAC" {
		t.Fatalf("categories = %+v", first.Categories)
	}
	if first.Link != "https://music.example/torrents/555" {
		t.Fatalf("link = %q", first.Link)
	}

	// Missing date degrades to the zero time, never an error.
	third := items[2]
	if !third.Published.IsZero() {
		t.Fatalf("published = %v, want zero", third.Published)
	}
	if third.Enclosure.URL != "" || third.Enclosure.Length != -1 {
		t.Fatalf("enclosure = %+v", third.Enclosure)
	}
}

func TestParseFeedRejectsGarbage(t *testing.T) {
	for _, data := range []string{"", "not xml at all", "<html><body>hi</body></html>", "<rss><channel><item>"} {
		if _, err := ParseFeed([]byte(data)); err == nil {
			t.Fatalf("expected error for %q", data)
		}
	}
}
