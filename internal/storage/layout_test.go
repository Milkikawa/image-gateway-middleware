package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareCleansPartFiles(t *testing.T) {
	root := t.TempDir()
	tmp := filepath.Join(root, "tmp")
	if err := os.MkdirAll(tmp, 0755); err != nil {
		t.Fatal(err)
	}
	part := filepath.Join(tmp, "x.part")
	if err := os.WriteFile(part, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(part); !os.IsNotExist(err) {
		t.Fatalf("part still exists: %v", err)
	}
}
