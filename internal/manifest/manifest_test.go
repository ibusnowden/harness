package manifest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildIgnoresLegacyTree(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "sample"), 0o755); err != nil {
		t.Fatalf("mkdir internal sample: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "legacy", "sample"), 0o755); err != nil {
		t.Fatalf("mkdir legacy sample: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "sample", "main.go"), []byte("package sample\n"), 0o644); err != nil {
		t.Fatalf("write active go file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "legacy", "sample", "old.go"), []byte("package sample\n"), 0o644); err != nil {
		t.Fatalf("write legacy go file: %v", err)
	}

	manifest, err := Build(root)
	if err != nil {
		t.Fatalf("build manifest: %v", err)
	}
	if manifest.TotalGoFiles != 1 {
		t.Fatalf("expected only active go files to count, got %d", manifest.TotalGoFiles)
	}
}
