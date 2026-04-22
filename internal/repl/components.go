package repl

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ThemedBox renders a bordered panel with optional header and padding.
func ThemedBox(theme Theme, content, header string, width, height int, focused bool) string {
	var lines []string
	if header != "" {
		lines = append(lines, theme.CardHeader().Width(max(8, width-4)).Render(header))
		lines = append(lines, theme.CardDivider().Render(strings.Repeat("─", max(8, width-4))))
		lines = append(lines, "")
	}
	lines = append(lines, content)
	rendered := strings.Join(lines, "\n")
	style := theme.Card()
	if focused {
		style = theme.CardFocused()
	}
	if width > 0 {
		style = style.Width(width)
	}
	if height > 0 {
		style = style.Height(height)
	}
	return style.Render(rendered)
}

// ThemedText renders styled text with automatic wrapping to the given width.
func ThemedText(theme Theme, style lipgloss.Style, text string, width int) string {
	if width <= 0 {
		return style.Render(text)
	}
	return style.Width(width).Render(text)
}

// ProgressBar renders a text-based progress bar with percentage.
func ProgressBar(theme Theme, percentage float64, barWidth int) string {
	if barWidth <= 0 {
		barWidth = 20
	}
	if percentage < 0 {
		percentage = 0
	}
	if percentage > 1 {
		percentage = 1
	}
	filled := int(float64(barWidth) * percentage)
	if filled > barWidth {
		filled = barWidth
	}
	empty := barWidth - filled
	pct := fmt.Sprintf("%.0f%%", percentage*100)
	bar := strings.Repeat("█", filled) + strings.Repeat("░", empty)
	if filled == 0 {
		bar = strings.Repeat("░", barWidth)
	}
	return theme.ProgressBar(percentage).Render(bar + " " + pct)
}

// StatusBadge renders a colored badge for permission/risk levels.
func StatusBadge(theme Theme, label string, bt BadgeType) string {
	var success, warning, error_ bool
	switch bt {
	case BadgeSuccess:
		success = true
	case BadgeWarning:
		warning = true
	case BadgeError:
		error_ = true
	}
	return theme.Badge(success, warning, error_).Render(label)
}

// TabItem represents a single tab in a tab bar.
type TabItem struct {
	Name string
}

// TabBar renders a tab bar with active/inactive states.
func TabBar(theme Theme, tabs []TabItem, activeIndex int, width int) string {
	if len(tabs) == 0 {
		return ""
	}
	var parts []string
	for i, tab := range tabs {
		var style lipgloss.Style
		if i == activeIndex {
			style = theme.TabActive()
		} else {
			style = theme.TabInactive()
		}
		parts = append(parts, style.Render(tab.Name))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, parts...)
}

// ShimmerText renders text with a simulated shimmer effect using ANSI bold toggle.
// When tick is even, text is bold; when odd, normal weight.
func ShimmerText(theme Theme, text string, tick int) string {
	if tick%2 == 0 {
		return theme.ShimmerStyle().Render(text)
	}
	return theme.Body().Render(text)
}

// Divider renders a horizontal rule using the card divider style.
func Divider(theme Theme, width int) string {
	if width <= 0 {
		width = 40
	}
	return theme.CardDivider().Render(strings.Repeat("─", min(width, 60)))
}

// Spacer renders a blank line for vertical spacing.
func Spacer() string {
	return ""
}

// MultiLine renders multiple lines with consistent styling.
func MultiLine(theme Theme, style lipgloss.Style, lines []string, width int) string {
	rendered := make([]string, len(lines))
	for i, line := range lines {
		rendered[i] = ThemedText(theme, style, line, width)
	}
	return strings.Join(rendered, "\n")
}
