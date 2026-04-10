package context

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type PortContext struct {
	SourceRoot     string
	TestRoot       string
	AssetsRoot     string
	GoFileCount    int
	TestFileCount  int
	AssetFileCount int
}

func Build(root string) (PortContext, error) {
	sourceRoot := filepath.Join(root, "internal")
	testRoot := root
	assetsRoot := filepath.Join(root, "assets")

	goFiles := 0
	testFiles := 0
	err := filepath.WalkDir(root, func(current string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			switch d.Name() {
			case ".cache", ".git", ".ascaris", "bin", "legacy":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".go") {
			goFiles++
			if strings.HasSuffix(d.Name(), "_test.go") {
				testFiles++
			}
		}
		return nil
	})
	if err != nil {
		return PortContext{}, err
	}
	assetFiles := 0
	_ = filepath.WalkDir(assetsRoot, func(current string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if !d.IsDir() {
			assetFiles++
		}
		return nil
	})
	return PortContext{
		SourceRoot:     sourceRoot,
		TestRoot:       testRoot,
		AssetsRoot:     assetsRoot,
		GoFileCount:    goFiles,
		TestFileCount:  testFiles,
		AssetFileCount: assetFiles,
	}, nil
}

func Render(ctx PortContext) string {
	lines := []string{
		"Source root: " + ctx.SourceRoot,
		"Test root: " + ctx.TestRoot,
		"Assets root: " + ctx.AssetsRoot,
		"Go files: " + strconv.Itoa(ctx.GoFileCount),
		"Test files: " + strconv.Itoa(ctx.TestFileCount),
		"Assets: " + strconv.Itoa(ctx.AssetFileCount),
	}
	return strings.Join(lines, "\n")
}
