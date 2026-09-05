package torrentfile

import (
	"strings"
	"testing"
)

func TestLayoutSizesAndSelections(t *testing.T) {
	raw := []byte("d4:infod6:lengthi100e4:name4:test12:piece lengthi16eee")
	layout, err := ParseLayout(raw)
	if err != nil || layout.Total != 100 || len(layout.Files) != 1 {
		t.Fatalf("layout: %+v %v", layout, err)
	}
	logical, pieces, err := SelectedBytes([]int64{10, 10, 10}, []bool{true, false, true}, 16)
	if err != nil || logical != 20 || pieces != 30 {
		t.Fatalf("overlapping boundary pieces: %d %d %v", logical, pieces, err)
	}
	logical, pieces, err = SelectedBytes([]int64{10, 10, 10}, []bool{false, true, false}, 16)
	if err != nil || logical != 10 || pieces != 30 {
		t.Fatal("skipped neighbors not included")
	}
	_, pieces, err = SelectedBytes([]int64{10, 10}, []bool{false, false}, 16)
	if err != nil || pieces != 0 {
		t.Fatal("all-skipped selection has demand")
	}
}
func TestLayoutRejectsTraversalAndParserBounds(t *testing.T) {
	for _, data := range []string{"d4:infod6:lengthi100e4:name2:..12:piece lengthi16eee", strings.Repeat("l", 130) + strings.Repeat("e", 130), "99999999999999999999999999:x"} {
		if _, err := ParseLayout([]byte(data)); err == nil {
			t.Fatal("unsafe metainfo accepted")
		}
	}
}
