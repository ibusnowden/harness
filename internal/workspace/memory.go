package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const memoryFile = "memory.md"

// MemoryPath returns the path to the workspace memory file.
func MemoryPath(root string) string {
	return filepath.Join(root, ".ascaris", memoryFile)
}

// ReadMemory returns the current memory content, or empty string if the file
// does not exist or is blank.
func ReadMemory(root string) string {
	data, err := os.ReadFile(MemoryPath(root))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// AppendMemory appends a timestamped note to the memory file, creating it if
// necessary.
func AppendMemory(root, note string) error {
	path := MemoryPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	ts := time.Now().Format("2006-01-02 15:04")
	_, err = fmt.Fprintf(f, "- [%s] %s\n", ts, strings.TrimSpace(note))
	return err
}

// ClearMemory truncates the memory file.
func ClearMemory(root string) error {
	path := MemoryPath(root)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	return os.WriteFile(path, nil, 0o644)
}
