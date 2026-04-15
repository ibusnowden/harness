package outputstyles

import (
	"fmt"
	"io"
	"math/rand"
	"os"
	"strings"
	"sync"
	"time"
)

const promptSpinnerInterval = 750 * time.Millisecond

var defaultPromptPhrases = []string{
	"Fuzzing",
	"Scanning",
	"Tracing",
	"Probing",
	"Hunting",
	"Analyzing",
	"Mapping",
	"Reversing",
	"Decompiling",
	"Diffing",
	"Bisecting",
	"Smashing",
	"Overflowing",
	"Leaking",
	"Pivoting",
	"Chaining",
	"Spraying",
	"Mutating",
	"Seeding",
	"Triaging",
	"Recon",
	"Enumerating",
	"Escalating",
	"Exploiting",
	"Patching",
	"Symbolizing",
	"Disassembling",
	"Crafting",
	"Replaying",
	"Grinding",
	"Weaponizing",
	"Executing",
	"Persisting",
	"Exfiltrating",
	"Delivering",
	"Controlling",
}

var defaultSpinnerFrames = []string{"-", "\\", "|", "/"}

type fdWriter interface {
	Fd() uintptr
}

type SpinnerConfig struct {
	Phrases  []string
	Frames   []string
	Interval time.Duration
	Rand     *rand.Rand
}

type PromptSpinner struct {
	writer      io.Writer
	phrases     []string
	frames      []string
	interval    time.Duration
	rng         *rand.Rand
	label       string
	phraseIndex int
	frameIndex  int
	lastWidth   int
	running     bool
	stopCh      chan struct{}
	doneCh      chan struct{}
	mu          sync.Mutex
}

func NewPromptSpinner(writer io.Writer) *PromptSpinner {
	return NewPromptSpinnerWithConfig(writer, SpinnerConfig{})
}

func NewPromptSpinnerWithConfig(writer io.Writer, cfg SpinnerConfig) *PromptSpinner {
	phrases := append([]string(nil), cfg.Phrases...)
	if len(phrases) == 0 {
		phrases = append([]string(nil), defaultPromptPhrases...)
	}
	frames := append([]string(nil), cfg.Frames...)
	if len(frames) == 0 {
		frames = append([]string(nil), defaultSpinnerFrames...)
	}
	interval := cfg.Interval
	if interval <= 0 {
		interval = promptSpinnerInterval
	}
	rng := cfg.Rand
	if rng == nil {
		rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	return &PromptSpinner{
		writer:   writer,
		phrases:  phrases,
		frames:   frames,
		interval: interval,
		rng:      rng,
	}
}

func DefaultPromptPhrases() []string {
	return append([]string(nil), defaultPromptPhrases...)
}

func IsInteractiveWriter(writer io.Writer) bool {
	fder, ok := writer.(fdWriter)
	if !ok {
		return false
	}
	file := os.NewFile(fder.Fd(), "")
	if file == nil {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func (s *PromptSpinner) Start(label string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return
	}
	s.label = normalizeLabel(label)
	s.frameIndex = 0
	s.phraseIndex = chooseStartIndex(len(s.phrases), s.rng)
	s.stopCh = make(chan struct{})
	s.doneCh = make(chan struct{})
	s.running = true
	s.renderLocked()
	go s.loop()
}

func (s *PromptSpinner) loop() {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	defer close(s.doneCh)
	for {
		select {
		case <-ticker.C:
			s.mu.Lock()
			if !s.running {
				s.mu.Unlock()
				return
			}
			s.frameIndex = nextIndex(s.frameIndex, len(s.frames))
			s.renderLocked()
			s.mu.Unlock()
		case <-s.stopCh:
			return
		}
	}
}

func (s *PromptSpinner) Update(label string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.label = normalizeLabel(label)
	if s.running {
		s.renderLocked()
	}
}

func (s *PromptSpinner) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	stopCh := s.stopCh
	doneCh := s.doneCh
	s.running = false
	s.mu.Unlock()

	close(stopCh)
	<-doneCh

	s.mu.Lock()
	s.clearLocked(true)
	s.mu.Unlock()
}

func chooseStartIndex(total int, rng *rand.Rand) int {
	if total <= 1 || rng == nil {
		return 0
	}
	return rng.Intn(total)
}

func nextIndex(current, total int) int {
	if total <= 1 {
		return 0
	}
	return (current + 1) % total
}

func normalizeLabel(label string) string {
	label = strings.TrimSpace(label)
	if label == "" {
		return "Working"
	}
	return label
}

func formatLine(frame, label, phrase string) string {
	line := fmt.Sprintf("[%s] %s", frame, normalizeLabel(label))
	if strings.TrimSpace(phrase) != "" {
		line += " " + strings.TrimSpace(phrase) + "..."
	}
	return line
}

func (s *PromptSpinner) renderLocked() {
	if s.writer == nil || len(s.frames) == 0 {
		return
	}
	line := formatLine(s.frames[s.frameIndex], s.label, s.currentPhrase())
	padding := ""
	if s.lastWidth > len(line) {
		padding = strings.Repeat(" ", s.lastWidth-len(line))
	}
	_, _ = fmt.Fprintf(s.writer, "\r%s%s", line, padding)
	s.lastWidth = len(line)
}

func (s *PromptSpinner) clearLocked(withNewline bool) {
	if s.writer == nil || s.lastWidth == 0 {
		return
	}
	_, _ = fmt.Fprintf(s.writer, "\r\033[2K")
	if withNewline {
		_, _ = fmt.Fprint(s.writer, "\n")
	} else {
		_, _ = fmt.Fprint(s.writer, "\r")
	}
	s.lastWidth = 0
}

func (s *PromptSpinner) currentPhrase() string {
	if len(s.phrases) == 0 {
		return ""
	}
	if s.phraseIndex < 0 || s.phraseIndex >= len(s.phrases) {
		s.phraseIndex = 0
	}
	return s.phrases[s.phraseIndex]
}
