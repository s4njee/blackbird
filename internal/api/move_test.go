package api

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestMovePathRenameAndCrossDeviceCopy(t *testing.T) {
	root := t.TempDir()
	makeSource := func(name, contents string) string {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	source := makeSource("rename-source", "rename")
	target := filepath.Join(root, "rename-target")
	if err := movePath(context.Background(), source, target); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("source still exists: %v", err)
	}
	if got, _ := os.ReadFile(target); string(got) != "rename" {
		t.Fatalf("target = %q", got)
	}

	source = makeSource("copy-source", "copy and verify")
	target = filepath.Join(root, "copy-target")
	if err := movePathWithRename(context.Background(), source, target, func(string, string) error { return syscall.EXDEV }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("cross-device source still exists: %v", err)
	}
	if got, _ := os.ReadFile(target); string(got) != "copy and verify" {
		t.Fatalf("cross-device target = %q", got)
	}
}

func TestMovePathPartialFailureAndCancellation(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.WriteFile(source, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("occupied"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := movePath(context.Background(), source, target); err == nil {
		t.Fatal("existing target unexpectedly moved")
	}
	if got, _ := os.ReadFile(source); string(got) != "keep me" {
		t.Fatalf("partial failure changed source: %q", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := movePathWithRename(ctx, source, filepath.Join(root, "cancelled"), func(string, string) error { return errors.New("should not rename") }); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v", err)
	}
}
