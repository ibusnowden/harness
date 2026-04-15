package repl

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ascarisLogoLines is the 6-row "ASCARIS" block-letter ASCII art.
// Generated from FIGlet "ANSI Shadow" font — stored as a constant, no runtime dep.
var ascarisLogoLines = [6]string{
	" █████╗ ███████╗ ██████╗ █████╗ ██████╗ ██╗███████╗",
	"██╔══██╗██╔════╝██╔════╝██╔══██╗██╔══██╗██║██╔════╝",
	"███████║███████╗██║     ███████║██████╔╝██║███████╗",
	"██╔══██║╚════██║██║     ██╔══██║██╔══██╗██║╚════██║",
	"██║  ██║███████║╚██████╗██║  ██║██║  ██║██║███████║",
	"╚═╝  ╚═╝╚══════╝ ╚═════╝╚═╝  ╚═╝╚═╝  ╚═╝╚═╝╚══════╝",
}

// ascarisLogoGradient maps each logo row to an ANSI 256-color code.
// Top rows are brightest gold (worm highlight), bottom rows deepen to amber/sienna.
var ascarisLogoGradient = [6]lipgloss.Color{
	"220", // bright gold     — highlight
	"214", // amber orange
	"172", // deep orange
	"208", // vivid orange
	"172", // deep orange
	"130", // dark burnt sienna — shadow
}

// renderLogo renders the 6-row gradient ASCII logo centered within width.
func renderLogo(width int, theme Theme) string {
	lines := make([]string, len(ascarisLogoLines))
	for i, row := range ascarisLogoLines {
		styled := lipgloss.NewStyle().Foreground(ascarisLogoGradient[i]).Bold(true).Render(row)
		lines[i] = lipgloss.PlaceHorizontal(width, lipgloss.Center, styled)
	}
	return strings.Join(lines, "\n")
}

// wormFrames is the animated header worm spinner.
// Keep it compact and circular so it reads as a single active motion source
// while the harness is working.
var wormFrames = []string{
	"◜",
	"◠",
	"◝",
	"◞",
	"◡",
	"◟",
}
