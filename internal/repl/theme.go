package repl

import "github.com/charmbracelet/lipgloss"

// Theme owns all visual configuration for the TUI.
// Colors are 256-color ANSI codes derived from the ascaris microscopy image:
// warm amber background, golden worm highlights, burnt sienna mid-tones.
// Obtain via DefaultTheme(); the zero value is not valid.
type Theme struct {
	ColorPrimary     string // "220" bright gold  — logo top, assistant label, prompt glyph
	ColorAccent      string // "214" amber orange  — focus border, active strip, worm glow
	ColorText        string // "216" warm peach    — body text
	ColorMeta        string // "179" warm tan      — metadata, secondary labels
	ColorDimmed      string // "137" brown-tan     — hints, help text
	ColorSuccess     string // "142" warm olive    — system/tool ok
	ColorError       string // "160" red           — errors
	ColorInputBg     string // "236" near-black    — modal background
	BorderColor      string // "172" deep orange   — unfocused panel border
	FocusBorderColor string // "220" bright gold   — focused panel border
	CompletionBg     string // "220" bright gold   — selected completion row background
	CompletionFg     string // "232" near-black    — selected completion row foreground
	CompletionBorder string // "240" dim grey      — completion overlay border
}

// DefaultTheme returns the ascaris worm color palette theme.
func DefaultTheme() Theme {
	return Theme{
		ColorPrimary:     "220",
		ColorAccent:      "214",
		ColorText:        "216",
		ColorMeta:        "179",
		ColorDimmed:      "137",
		ColorSuccess:     "142",
		ColorError:       "160",
		ColorInputBg:     "236",
		BorderColor:      "172",
		FocusBorderColor: "220",
		CompletionBg:     "220",
		CompletionFg:     "232",
		CompletionBorder: "240",
	}
}

func (t Theme) Primary() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(t.ColorPrimary)).Bold(true)
}

func (t Theme) Accent() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(t.ColorAccent)).Bold(true)
}

func (t Theme) Body() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(t.ColorText))
}

func (t Theme) Meta() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(t.ColorMeta))
}

func (t Theme) Help() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(t.ColorDimmed))
}

func (t Theme) Success() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(t.ColorSuccess)).Bold(true)
}

func (t Theme) Err() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(t.ColorError)).Bold(true)
}

func (t Theme) Frame() lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(t.BorderColor)).
		Padding(0, 1)
}

func (t Theme) FocusFrame() lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(t.FocusBorderColor)).
		Padding(0, 1)
}

func (t Theme) CompletionSelected() lipgloss.Style {
	return lipgloss.NewStyle().
		Background(lipgloss.Color(t.CompletionBg)).
		Foreground(lipgloss.Color(t.CompletionFg)).
		Bold(true)
}

func (t Theme) CompletionRow() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(t.ColorMeta))
}

func (t Theme) CompletionFrame() lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(t.CompletionBorder)).
		Padding(0, 1)
}

func (t Theme) Modal() lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(t.ColorPrimary)).
		Padding(1, 2).
		Background(lipgloss.Color(t.ColorInputBg))
}
