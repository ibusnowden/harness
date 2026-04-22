package api

import (
	"fmt"
	"unicode/utf8"
)

// MaxToolOutputChars is the per-tool-call cap on Output bytes stored in the
// message history. Tool output above this size is replaced with a
// head…elision…tail form so the model still sees both ends but a single
// runaway bash or read_file call cannot single-handedly exhaust the context
// window. 8000 bytes is ~2000 tokens under the char/4 heuristic.
const MaxToolOutputChars = 8000

// TruncateToolOutput caps output at maxChars using a middle-elision. The
// returned string is safe to store as a ToolResultEnvelope.Output: it
// preserves both the start and the end of the original (most tool output
// either frontloads context or backloads the answer, so cutting the middle
// is the least destructive default).
func TruncateToolOutput(output string, maxChars int) string {
	return TruncateMiddle(output, maxChars)
}

// TruncateMiddle returns value unchanged when it is maxChars bytes or fewer,
// and otherwise replaces the middle with a "[truncated N chars]" marker. Cuts
// on rune boundaries so a multibyte UTF-8 sequence is never split.
func TruncateMiddle(value string, maxChars int) string {
	if maxChars <= 0 || len(value) <= maxChars {
		return value
	}
	head := maxChars / 2
	tail := maxChars - head
	headEnd := runeSafeBoundary(value, head, false)
	tailStart := runeSafeBoundary(value, len(value)-tail, true)
	if tailStart <= headEnd {
		return value
	}
	omitted := tailStart - headEnd
	return value[:headEnd] + fmt.Sprintf("\n... [truncated %d chars] ...\n", omitted) + value[tailStart:]
}

// runeSafeBoundary nudges a byte index to the nearest rune boundary. When
// forward is false we move backward (so the head slice ends at a rune edge);
// when forward is true we move forward (so the tail slice begins at one).
func runeSafeBoundary(s string, i int, forward bool) int {
	if i <= 0 {
		return 0
	}
	if i >= len(s) {
		return len(s)
	}
	if forward {
		for i < len(s) && !utf8.RuneStart(s[i]) {
			i++
		}
		return i
	}
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return i
}
