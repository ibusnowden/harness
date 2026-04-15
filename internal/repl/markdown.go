package repl

import (
	"strings"

	"github.com/charmbracelet/glamour"
)

// markdownRenderer wraps glamour for rendering assistant responses in the
// transcript viewport. Created once at model init and resized on layout changes.
type markdownRenderer struct {
	r     *glamour.TermRenderer
	width int
}

func newMarkdownRenderer(width int) *markdownRenderer {
	m := &markdownRenderer{width: width}
	m.init(width)
	return m
}

func (m *markdownRenderer) init(width int) {
	wordWrap := width - 4
	if wordWrap < 40 {
		wordWrap = 40
	}
	// Use a fixed dark style — WithAutoStyle() sends an OSC 11 terminal query
	// to detect background color, but inside Bubble Tea's alt-screen the
	// response lands in the textarea as raw escape bytes (]11;rgb:...).
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(wordWrap),
	)
	if err != nil {
		m.r = nil
		return
	}
	m.r = r
}

// Render renders markdown src to an ANSI string.
// Falls back to returning src unchanged if the renderer is unavailable.
func (m *markdownRenderer) Render(src string) string {
	if m.r == nil {
		return src
	}
	out, err := m.r.Render(src)
	if err != nil {
		return src
	}
	// glamour appends a trailing newline; strip it for consistent block layout
	return strings.TrimRight(out, "\n")
}

// Resize recreates the renderer for a new terminal width.
// Called from model.layout() whenever the terminal is resized.
func (m *markdownRenderer) Resize(width int) {
	if width == m.width {
		return
	}
	m.width = width
	m.init(width)
}
