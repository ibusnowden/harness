package api

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateToolOutput_LeavesShortOutputUnchanged(t *testing.T) {
	for _, in := range []string{"", "hi", strings.Repeat("x", MaxToolOutputChars-1), strings.Repeat("y", MaxToolOutputChars)} {
		got := TruncateToolOutput(in, MaxToolOutputChars)
		if got != in {
			t.Fatalf("expected passthrough for len=%d, got modified (len=%d)", len(in), len(got))
		}
	}
}

func TestTruncateToolOutput_TruncatesLongOutput(t *testing.T) {
	input := strings.Repeat("abcd", 5000)
	got := TruncateToolOutput(input, MaxToolOutputChars)
	if len(got) >= len(input) {
		t.Fatalf("expected truncation, got len=%d original=%d", len(got), len(input))
	}
	if !strings.Contains(got, "truncated") {
		t.Fatalf("expected truncation marker, got %q", got[:80])
	}
	// Must preserve both the original start and end so the model sees both
	// ends of the output.
	if !strings.HasPrefix(got, input[:100]) {
		t.Fatalf("expected head to be preserved, got prefix %q", got[:100])
	}
	if !strings.HasSuffix(got, input[len(input)-100:]) {
		t.Fatalf("expected tail to be preserved, got suffix %q", got[len(got)-100:])
	}
}

func TestTruncateMiddle_ZeroMaxCharsReturnsInput(t *testing.T) {
	in := "some value"
	if got := TruncateMiddle(in, 0); got != in {
		t.Fatalf("expected passthrough when maxChars is 0, got %q", got)
	}
	if got := TruncateMiddle(in, -1); got != in {
		t.Fatalf("expected passthrough when maxChars is negative, got %q", got)
	}
}

func TestTruncateMiddle_RuneSafeOnMultibyteInput(t *testing.T) {
	// Mix of 2-, 3-, and 4-byte UTF-8 runes, long enough to force truncation.
	parts := []string{"αβγδεζηθικλμνξοπρστυφχψω", "日本語テキスト", "🙂🚀🎯🔥"}
	in := strings.Repeat(strings.Join(parts, " | "), 200)
	if len(in) <= 1000 {
		t.Fatalf("test setup bug: input len=%d is not long enough", len(in))
	}
	got := TruncateMiddle(in, 1000)
	if !utf8.ValidString(got) {
		t.Fatalf("truncation produced invalid UTF-8")
	}
	if len(got) >= len(in) {
		t.Fatalf("expected truncation, got len=%d", len(got))
	}
}
