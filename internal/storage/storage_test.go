package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestAliasesShareFilesystemAndUnavailableRootIsUnknown(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	os.Mkdir(nested, 0700)
	alias := filepath.Join(root, "alias")
	os.Symlink(nested, alias)
	in := Inspector{Roots: []string{root, nested}}
	a, err := in.Resolve(nested)
	if err != nil {
		t.Fatal(err)
	}
	b, err := in.Resolve(filepath.Join(alias, "future", "data"))
	if err != nil {
		t.Fatal(err)
	}
	p := NewPlan()
	p.Append(a, 50, Operation{Upper: Number(100)})
	p.Append(b, 50, Operation{Upper: Number(200)})
	p.Finish()
	if len(p.Pools) != 1 || *p.Pools[0].Upper != 300 || p.Pools[0].Reserve != 50 {
		t.Fatalf("alias counted twice: %+v", p.Pools)
	}
	missing := filepath.Join(root, "missing-mount")
	in.Roots = append(in.Roots, missing)
	if _, err := in.Resolve(filepath.Join(missing, "data")); err == nil {
		t.Fatal("unavailable mount became parent capacity")
	}
	outside := t.TempDir()
	os.Symlink(outside, filepath.Join(root, "escape"))
	if _, err := in.Resolve(filepath.Join(root, "escape", "missing", "file")); err == nil {
		t.Fatal("nested missing symlink escaped roots")
	}
}
func TestAllocatedBlocksAndSparseCopyAreDifferent(t *testing.T) {
	root := t.TempDir()
	dense := filepath.Join(root, "dense")
	data := make([]byte, 2<<20)
	for i := range data {
		data[i] = byte(i % 251)
	}
	if err := os.WriteFile(dense, data, 0600); err != nil {
		t.Fatal(err)
	}
	in := Inspector{Roots: []string{root}}
	credit, err := in.Allocation(context.Background(), dense, int64(len(data)))
	if err != nil || credit != int64(len(data)) {
		t.Fatalf("preallocated bytes not credited: %d %v", credit, err)
	}
	sparse := filepath.Join(root, "sparse")
	f, err := os.Create(sparse)
	if err != nil {
		t.Fatal(err)
	}
	f.Truncate(64 << 20)
	f.Close()
	credit, err = in.Allocation(context.Background(), sparse, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	logical, allocated, err := in.CopySize(context.Background(), sparse)
	if err != nil || logical != 64<<20 || allocated != credit {
		t.Fatalf("copy must model logical sparse bytes: %d %d %v", logical, allocated, err)
	}
	if credit >= logical {
		t.Skip("filesystem materialized sparse file")
	}
	p := NewPlan()
	pool := Pool{ID: "destination", Total: 1000, Free: 500}
	p.Append(pool, 20, Operation{Description: "Copy before delete", Logical: 600, Allocated: 10, Lower: 600, Upper: Number(600)})
	p.Finish()
	if p.Pools[0].Status != "insufficient" || *p.Pools[0].Peak != 1100 {
		t.Fatal("copy peak lost source/destination overlap")
	}
}
func TestUnknownExpansionAndConcurrentPeak(t *testing.T) {
	p := NewPlan()
	pool := Pool{ID: "one", Total: 1000, Free: 600}
	p.Append(pool, 100, Operation{Description: "Download", Upper: Number(200)})
	p.Append(pool, 100, Operation{Description: "Same filesystem rename", Upper: Number(0)})
	p.Append(pool, 100, Operation{Description: "Extraction", Upper: nil})
	p.Finish()
	if p.Pools[0].Upper != nil || p.Pools[0].Peak != nil || p.Pools[0].Status != "unknown" {
		t.Fatal("unknown expansion treated as zero")
	}
	bounded := NewPlan()
	bounded.Append(pool, 100, Operation{Upper: Number(200)})
	bounded.Append(pool, 100, Operation{Description: "Assumed extraction", Upper: Number(400)})
	bounded.Finish()
	if *bounded.Pools[0].Upper != 600 || bounded.Pools[0].Status != "at_risk" {
		t.Fatal("concurrent extraction demand omitted")
	}
}

func TestMoveDemandDistinguishesSharedCapacityFromRenameMount(t *testing.T) {
	source := Pool{ID: "same-device", Mount: "mount-one"}
	alias := Pool{ID: "same-device", Mount: "mount-one"}
	bind := Pool{ID: "same-device", Mount: "mount-two"}
	other := Pool{ID: "other-device", Mount: "mount-three"}
	if got := MoveDemand(source, alias, 1000, 100); *got.Upper != 0 || got.Lower != 0 {
		t.Fatal("same mount rename became a copy")
	}
	for _, target := range []Pool{bind, other} {
		got := MoveDemand(source, target, 1000, 100)
		if *got.Upper != 1000 || got.Lower != 1000 {
			t.Fatal("copy demand used allocated instead of logical bytes")
		}
	}
	if got := MoveDemand(source, Pool{ID: source.ID}, 1000, 100); got.Lower != 0 || *got.Upper != 1000 {
		t.Fatal("unknown mount dropped fallback demand")
	}
	mounts := parseMounts("1 0 8:1 / / rw - ext4 /dev/a rw\n2 1 8:1 /data /bind\\040alias rw - ext4 /dev/a rw\n")
	if mountFor("/data/file", mounts) != "1" || mountFor("/bind alias/file", mounts) != "2" {
		t.Fatal("mount identity/escaped path parsing")
	}
}
