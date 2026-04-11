package outputstyles

import (
	"bytes"
	"math/rand"
	"strings"
	"testing"
)

func TestDefaultPromptPhrasesRemainDefensive(t *testing.T) {
	phrases := DefaultPromptPhrases()
	if len(phrases) == 0 {
		t.Fatalf("expected default prompt phrases")
	}
	banned := []string{
		"Zero-daying",
		"Privilege-escalating",
		"Shellcode-injecting",
		"WAF-bypassing",
		"C2-beaconing",
		"Malware-embedding",
		"Ransomware-encrypting",
		"Data-exfiltrating",
	}
	for _, item := range banned {
		for _, phrase := range phrases {
			if phrase == item {
				t.Fatalf("unexpected offensive phrase in default set: %q", item)
			}
		}
	}
}

func TestSpinnerIndexSelectionAndWrap(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	index := chooseStartIndex(3, rng)
	if index < 0 || index >= 3 {
		t.Fatalf("unexpected start index: %d", index)
	}
	if nextIndex(2, 3) != 0 {
		t.Fatalf("expected wrapped index")
	}
}

func TestSpinnerRenderAndClearLine(t *testing.T) {
	var buffer bytes.Buffer
	spinner := NewPromptSpinnerWithConfig(&buffer, SpinnerConfig{
		Phrases: []string{"Fuzzing"},
		Frames:  []string{"-"},
	})
	spinner.label = "Thinking"
	spinner.renderLocked()
	spinner.clearLocked()

	line := formatLine("-", "Thinking", "Fuzzing")
	output := buffer.String()
	if !strings.Contains(output, "\r"+line) {
		t.Fatalf("expected rendered line, got %q", output)
	}
	if !strings.Contains(output, strings.Repeat(" ", len(line))) {
		t.Fatalf("expected clear sequence, got %q", output)
	}
}
