package context

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildIgnoresLegacyTreeAndRenderIsGoFirst(t *testing.T) {
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

	ctx, err := Build(root)
	if err != nil {
		t.Fatalf("build context: %v", err)
	}
	if ctx.GoFileCount != 1 {
		t.Fatalf("expected only active go files to count, got %d", ctx.GoFileCount)
	}
	rendered := Render(ctx)
	if strings.Contains(rendered, "Archive ") {
		t.Fatalf("expected Go-first context render, got %q", rendered)
	}
}
