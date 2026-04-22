package repl

import (
	"time"

	"github.com/charmbracelet/lipgloss"
)

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

	// Card/panel styling
	CardBg         string // "235" slightly lighter near-black for card backgrounds
	CardBorder     string // "238" subtle grey for card borders
	CardFocusBorder string // "220" bright gold for focused card borders
	CardHeaderBg   string // "236" same as input bg for header strip
	CardHeaderFg   string // "220" bright gold for card headers
	CardDividerColor    string // "238" subtle grey for horizontal rules
	CardTipFgColor      string // "216" warm peach for tip items
	CardTipIconFgColor  string // "214" amber for tip icons/bullets

	// Status badge styling
	BadgeBg        string // "236" near-black for badge backgrounds
	BadgeSuccessBg string // "52" dark green for success badges
	BadgeWarningBg string // "94" muted blue for info badges
	BadgeErrorBg   string // "52" dark red for error badges

	// Progress bar
	ProgressFg   string // "220" bright gold for filled progress
	ProgressBg   string // "237" dark grey for empty progress
	ProgressCursor string // "214" amber orange for cursor

	// Composer
	ComposerBorderFocused string // "220" bright gold for focused composer
	ComposerBorderIdle    string // "238" grey for idle composer
	ComposerCwdFg         string // "137" brown-tan for cwd line
	ComposerPlaceholderFg string // "240" dim grey for placeholder text

	// Shimmer/animation
	ShimmerInterval time.Duration
}

// DefaultTheme returns the ascaris worm color palette theme.
func DefaultTheme() Theme {
	return Theme{
		ColorPrimary:      "220",
		ColorAccent:       "214",
		ColorText:         "216",
		ColorMeta:         "179",
		ColorDimmed:       "137",
		ColorSuccess:      "142",
		ColorError:        "160",
		ColorInputBg:      "236",
		BorderColor:       "172",
		FocusBorderColor:  "220",
		CompletionBg:      "220",
		CompletionFg:      "232",
		CompletionBorder:  "240",
		CardBg:            "235",
		CardBorder:        "238",
		CardFocusBorder:   "220",
		CardHeaderBg:      "236",
		CardHeaderFg:      "220",
		CardDividerColor:    "238",
		CardTipFgColor:      "216",
		CardTipIconFgColor:  "214",
		BadgeBg:           "236",
		BadgeSuccessBg:    "52",
		BadgeWarningBg:    "94",
		BadgeErrorBg:      "52",
		ProgressFg:        "220",
		ProgressBg:        "237",
		ProgressCursor:    "214",
		ComposerBorderFocused: "220",
		ComposerBorderIdle:    "238",
		ComposerCwdFg:         "137",
		ComposerPlaceholderFg: "240",
		ShimmerInterval:       300 * time.Millisecond,
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

// Card returns an unfocused card/panel style with subtle border.
func (t Theme) Card() lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(t.CardBorder)).
		Padding(0, 1).
		Background(lipgloss.Color(t.CardBg))
}

// CardFocused returns a focused card/panel style with bright border.
func (t Theme) CardFocused() lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(t.CardFocusBorder)).
		Padding(0, 1).
		Background(lipgloss.Color(t.CardBg))
}

// CardHeader returns a card header strip style.
func (t Theme) CardHeader() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(t.CardHeaderFg)).
		Bold(true).
		Padding(0, 1)
}

// CardDivider returns a horizontal divider style.
func (t Theme) CardDivider() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(t.CardDividerColor))
}

// BadgeType represents the type of status badge.
type BadgeType int

const (
	BadgeDefault BadgeType = iota
	BadgeSuccess
	BadgeWarning
	BadgeError
)

// Badge returns a styled badge for permission/risk levels.
func (t Theme) Badge(success, warning, error bool) lipgloss.Style {
	var bg string
	switch {
	case error:
		bg = t.BadgeErrorBg
	case success:
		bg = t.BadgeSuccessBg
	case warning:
		bg = t.BadgeWarningBg
	default:
		bg = t.BadgeBg
	}
	return lipgloss.NewStyle().
		Background(lipgloss.Color(bg)).
		Foreground(lipgloss.Color(t.ColorText)).
		Bold(true).
		Padding(0, 1)
}

// ProgressBar renders a text-based progress bar style.
func (t Theme) ProgressBar(percentage float64) lipgloss.Style {
	if percentage > 0.5 {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(t.ProgressFg)).Bold(true)
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(t.ProgressBg))
}

// ComposerBorder returns the composer border style based on focus state.
func (t Theme) ComposerBorder(focused bool) lipgloss.Style {
	border := t.CardBorder
	if focused {
		border = t.ComposerBorderFocused
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(border)).
		Padding(0, 1)
}

// ComposerCwd returns the cwd line style.
func (t Theme) ComposerCwd() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(t.ComposerCwdFg))
}

// ComposerPlaceholder returns the placeholder text style.
func (t Theme) ComposerPlaceholder() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(t.ComposerPlaceholderFg))
}

// ShimmerStyle returns a style suitable for shimmer animation.
func (t Theme) ShimmerStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(t.ColorPrimary)).Bold(true)
}

// TabBar returns the tab bar container style.
func (t Theme) TabBar() lipgloss.Style {
	return lipgloss.NewStyle().Padding(0, 1)
}

// TabActive returns the active tab style.
func (t Theme) TabActive() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(t.ColorPrimary)).
		Bold(true)
}

// TabInactive returns the inactive tab style.
func (t Theme) TabInactive() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(t.ColorMeta))
}

// CardTipFg returns the tip text foreground style.
func (t Theme) CardTipFg() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(t.CardTipFgColor))
}

// CardTipIconFg returns the tip icon foreground style.
func (t Theme) CardTipIconFg() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(t.CardTipIconFgColor))
}
