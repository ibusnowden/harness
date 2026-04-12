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
	expected := []string{
		"Bombaclatt",
		"Paibraniang",
		"Fuzzing",
		"fkTheyTalkingAbout",
		"Start-living",
	}
	for _, item := range expected {
		found := false
		for _, phrase := range phrases {
			if phrase == item {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected phrase in default set: %q", item)
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
	spinner.clearLocked(false)

	line := formatLine("-", "Thinking", "Fuzzing")
	output := buffer.String()
	if !strings.Contains(output, "\r"+line) {
		t.Fatalf("expected rendered line, got %q", output)
	}
	if !strings.Contains(output, "\r\x1b[2K\r") {
		t.Fatalf("expected clear sequence, got %q", output)
	}
}

func TestSpinnerStopAdvancesToCleanLine(t *testing.T) {
	var buffer bytes.Buffer
	spinner := NewPromptSpinnerWithConfig(&buffer, SpinnerConfig{
		Phrases: []string{"Fuzzing"},
		Frames:  []string{"-"},
	})
	spinner.Start("Thinking")
	spinner.Stop()

	output := buffer.String()
	if !strings.Contains(output, "\r\x1b[2K\n") {
		t.Fatalf("expected stop to clear line and advance, got %q", output)
	}
}

func TestSpinnerKeepsRandomPhraseFixedWhileFramesAdvance(t *testing.T) {
	var buffer bytes.Buffer
	spinner := NewPromptSpinnerWithConfig(&buffer, SpinnerConfig{
		Phrases: []string{"Bombaclatt", "Paibraniang", "Snodening"},
		Frames:  []string{"-", "\\"},
	})
	spinner.label = "Thinking"
	spinner.phraseIndex = 1
	spinner.frameIndex = 0

	spinner.renderLocked()
	firstPhrase := spinner.currentPhrase()
	spinner.frameIndex = nextIndex(spinner.frameIndex, len(spinner.frames))
	spinner.renderLocked()
	secondPhrase := spinner.currentPhrase()

	if firstPhrase != secondPhrase {
		t.Fatalf("expected phrase to remain fixed, got %q then %q", firstPhrase, secondPhrase)
	}
	output := buffer.String()
	if !strings.Contains(output, "Paibraniang...") {
		t.Fatalf("expected rendered phrase with uniform suffix, got %q", output)
	}
}

func TestSpinnerUpdateChangesLabelWithoutChangingPhrase(t *testing.T) {
	var buffer bytes.Buffer
	spinner := NewPromptSpinnerWithConfig(&buffer, SpinnerConfig{
		Phrases: []string{"Bombaclatt", "Paibraniang", "Snodening"},
		Frames:  []string{"-"},
		Rand:    rand.New(rand.NewSource(0)),
	})

	spinner.Start("Starting")
	selected := spinner.currentPhrase()
	spinner.Update("Thinking")
	spinner.Stop()

	if spinner.currentPhrase() != selected {
		t.Fatalf("expected phrase to persist across label update, got %q then %q", selected, spinner.currentPhrase())
	}
	output := buffer.String()
	if !strings.Contains(output, "Starting "+selected+"...") {
		t.Fatalf("expected initial label render, got %q", output)
	}
	if !strings.Contains(output, "Thinking "+selected+"...") {
		t.Fatalf("expected updated label render with same phrase, got %q", output)
	}
}
