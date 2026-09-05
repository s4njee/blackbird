package torrentfile

import (
	"strings"
	"testing"
	"time"
)

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// tstr emits a bencode string token: "<len>:<value>".
func tstr(s string) string { return itoa(len(s)) + ":" + s }

func TestParseFullMetadata(t *testing.T) {
	// info dict (keys may appear in any order; re-encoding sorts them).
	info := "d" +
		tstr("length") + "i6474842112e" +
		tstr("name") + tstr("ubuntu-24.04.2.iso") +
		tstr("piece length") + "i16384e" +
		tstr("private") + "i1e" +
		tstr("pieces") + tstr("12345678901234567890") +
		"e"
	root := "d" +
		tstr("announce") + tstr("http://tracker.example/announce") +
		tstr("comment") + tstr("Example release") +
		tstr("created by") + tstr("ExampleTool 2.0.1") +
		tstr("creation date") + "i1756700300e" +
		tstr("info") + info +
		"e"
	meta, err := Parse([]byte(root))
	if err != nil {
		t.Fatal(err)
	}
	if meta.Name != "ubuntu-24.04.2.iso" {
		t.Errorf("name = %q", meta.Name)
	}
	if meta.Comment != "Example release" {
		t.Errorf("comment = %q", meta.Comment)
	}
	if meta.CreatedBy != "ExampleTool 2.0.1" {
		t.Errorf("createdBy = %q", meta.CreatedBy)
	}
	if meta.CreationDate == nil {
		t.Fatal("creationDate is nil")
	}
	if want := time.Unix(1756700300, 0); !meta.CreationDate.Equal(want) {
		t.Errorf("creationDate = %v, want %v", *meta.CreationDate, want)
	}
	if !meta.Private {
		t.Error("private = false, want true")
	}
	if len(meta.Infohash) != 40 {
		t.Fatalf("infohash = %q", meta.Infohash)
	}
}

func TestParseMinimalAndMissingOptionals(t *testing.T) {
	meta, err := Parse([]byte("d" + tstr("info") + "d" + tstr("name") + tstr("test") + "ee"))
	if err != nil {
		t.Fatal(err)
	}
	if meta.Name != "test" {
		t.Errorf("name = %q", meta.Name)
	}
	if meta.Comment != "" || meta.CreatedBy != "" || meta.Private {
		t.Errorf("optional fields should be zero: %+v", meta)
	}
	if meta.CreationDate != nil {
		t.Errorf("creationDate = %v, want nil", meta.CreationDate)
	}
	if len(meta.Infohash) != 40 {
		t.Errorf("infohash = %q", meta.Infohash)
	}
}

func TestParseInfohashStableAcrossKeyOrdering(t *testing.T) {
	// The same info dict with keys in different physical order must hash
	// identically because re-encoding sorts dict keys.
	a := ParseOrFatal(t, "d"+tstr("info")+"d"+tstr("length")+"i1e"+tstr("name")+tstr("x")+tstr("pieces")+tstr("z")+"ee")
	b := ParseOrFatal(t, "d"+tstr("info")+"d"+tstr("name")+tstr("x")+tstr("length")+"i1e"+tstr("pieces")+tstr("z")+"ee")
	if a.Infohash != b.Infohash {
		t.Fatalf("infohash differs by key order: %s vs %s", a.Infohash, b.Infohash)
	}
}

func TestParseErrors(t *testing.T) {
	valid := "d" + tstr("info") + "d" + tstr("name") + tstr("x") + "ee"
	cases := map[string][]byte{
		"empty":     {},
		"not dict":  []byte("i1e"),
		"no info":   []byte("d" + tstr("name") + tstr("x") + "e"),
		"truncated": []byte("d" + tstr("info") + "d" + tstr("name") + tstr("t")),
		"trailing":  []byte(valid + "x"),
	}
	for name, data := range cases {
		if _, err := Parse(data); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestHasInfohash(t *testing.T) {
	if !HasInfohash("abcd1234abcd1234abcd1234abcd1234abcd1234") {
		t.Error("valid hex infohash rejected")
	}
	if HasInfohash("short") || HasInfohash(strings.Repeat("g", 40)) {
		t.Error("invalid infohash accepted")
	}
}

func ParseOrFatal(t *testing.T, bencode string) *Meta {
	t.Helper()
	meta, err := Parse([]byte(bencode))
	if err != nil {
		t.Fatal(err)
	}
	return meta
}
