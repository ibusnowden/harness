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

// Worm art: a single thick ascaris worm body tracing a fluid S-curve,
// inspired by actual ascaris microscopy — one continuous worm with an upper
// arc curving left and a lower arc curving right.
//
// Grid: 13 cols × 17 rows. Each step owns 3 horizontal cells (thick body).
// A peristaltic gold pulse travels head→tail giving the crawling illusion.
//
// Shape preview (■ = body cells, path goes top→bottom):
//   Row  0:  . . . . . . . ■ ■ ■ . . .   ← head
//   Row  1:  . . . . . . ■ ■ ■ . . . .
//   Row  2:  . . . . . ■ ■ ■ . . . . .
//   Row  3:  . . . . ■ ■ ■ . . . . . .
//   Row  4:  . . . ■ ■ ■ . . . . . . .   ← leftmost (upper arc)
//   Row  5:  . . . . ■ ■ ■ . . . . . .
//   Row  6:  . . . . . ■ ■ ■ . . . . .
//   Row  7:  . . . . . . ■ ■ ■ . . . .
//   Row  8:  . . . . . . . ■ ■ ■ . . .   ← inflection (center)
//   Row  9:  . . . . . . . . ■ ■ ■ . .
//   Row 10:  . . . . . . . . . ■ ■ ■ .
//   Row 11:  . . . . . . . . . . ■ ■ ■   ← rightmost (lower arc)
//   Row 12:  . . . . . . . . . ■ ■ ■ .
//   Row 13:  . . . . . . . . ■ ■ ■ . .
//   Row 14:  . . . . . . . ■ ■ ■ . . .
//   Row 15:  . . . . . . ■ ■ ■ . . . .
//   Row 16:  . . . . . ■ ■ ■ . . . . .   ← tail

const (
	wormArtRows      = 17
	wormArtCols      = 13
	wormAnimTotal    = 17
	wormCellsPerStep = 3 // cells per step — body thickness
)

// wormPath traces the single S-shaped worm body from head (row 0) to tail
// (row 16). Every 3 consecutive entries share the same animation step index
// (step = i / wormCellsPerStep).
var wormPath = [][2]int{
	{0, 7}, {0, 8}, {0, 9}, // step 0  — head
	{1, 6}, {1, 7}, {1, 8}, // step 1
	{2, 5}, {2, 6}, {2, 7}, // step 2
	{3, 4}, {3, 5}, {3, 6}, // step 3
	{4, 3}, {4, 4}, {4, 5}, // step 4  — leftmost (upper arc apex)
	{5, 4}, {5, 5}, {5, 6}, // step 5
	{6, 5}, {6, 6}, {6, 7}, // step 6
	{7, 6}, {7, 7}, {7, 8}, // step 7
	{8, 7}, {8, 8}, {8, 9}, // step 8  — inflection (center)
	{9, 8}, {9, 9}, {9, 10}, // step 9
	{10, 9}, {10, 10}, {10, 11}, // step 10
	{11, 10}, {11, 11}, {11, 12}, // step 11 — rightmost (lower arc apex)
	{12, 9}, {12, 10}, {12, 11}, // step 12
	{13, 8}, {13, 9}, {13, 10}, // step 13
	{14, 7}, {14, 8}, {14, 9}, // step 14
	{15, 6}, {15, 7}, {15, 8}, // step 15
	{16, 5}, {16, 6}, {16, 7}, // step 16 — tail
}

// wormArtPalette: bright gold at phase 0 (wave peak), dark at phases 7-9.
// The wave travels head→tail as animFrame increments.
var wormArtPalette = [wormAnimTotal]lipgloss.Color{
	"220", // 0  — bright gold peak
	"220", // 1
	"214", // 2  — amber
	"214", // 3
	"172", // 4  — deep orange
	"130", // 5  — dark sienna
	"94",  // 6  — darkest
	"94",  // 7
	"94",  // 8
	"130", // 9  — recovering
	"130", // 10
	"172", // 11
	"172", // 12
	"214", // 13
	"220", // 14 — back to bright
	"220", // 15
	"214", // 16
}

// wormArtStyles pre-builds lipgloss styles for each palette entry.
var wormArtStyles = func() [wormAnimTotal]lipgloss.Style {
	var s [wormAnimTotal]lipgloss.Style
	for i, c := range wormArtPalette {
		s[i] = lipgloss.NewStyle().Foreground(c)
	}
	return s
}()

// renderWormAnim renders the S-shaped worm with an animated peristaltic gold
// wave flowing head→tail. animFrame cycles 0..wormAnimTotal-1.
// Each cell renders as "▪ " (2 terminal chars); total art width = 26 chars.
func renderWormAnim(animFrame, width int) string {
	type cell struct {
		phase int
		set   bool
	}
	var grid [wormArtRows][wormArtCols]cell

	for i, pos := range wormPath {
		r, c := pos[0], pos[1]
		step := i / wormCellsPerStep
		phase := ((step - animFrame) % wormAnimTotal + wormAnimTotal) % wormAnimTotal
		if !grid[r][c].set || phase < grid[r][c].phase {
			grid[r][c] = cell{phase: phase, set: true}
		}
	}

	lines := make([]string, wormArtRows)
	for r := 0; r < wormArtRows; r++ {
		var sb strings.Builder
		for c := 0; c < wormArtCols; c++ {
			if !grid[r][c].set {
				sb.WriteString("  ")
			} else {
				sb.WriteString(wormArtStyles[grid[r][c].phase].Render("▪ "))
			}
		}
		lines[r] = lipgloss.PlaceHorizontal(width, lipgloss.Center, sb.String())
	}
	return strings.Join(lines, "\n")
}
