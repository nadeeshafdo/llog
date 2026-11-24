package main

import (
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func stripAnsi(s string) string {
	return sgrRe.ReplaceAllString(s, "")
}

func TestWrapBasic(t *testing.T) {
	got := wrapLine("aaa bbb ccc", 7)
	want := []string{"aaa bbb", "ccc"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %q", got)
	}
}

func TestWrapLongWord(t *testing.T) {
	got := wrapLine("abcdefghij", 4)
	want := []string{"abcd", "efgh", "ij"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q", got)
	}
}

func TestWrapEmpty(t *testing.T) {
	if got := wrapLine("", 10); len(got) != 1 || got[0] != "" {
		t.Fatalf("got %q", got)
	}
	if got := wrapLine("abc", 0); len(got) != 1 || got[0] != "" {
		t.Fatalf("got %q", got)
	}
}

func TestWrapAnsiKeptIntact(t *testing.T) {
	got := wrapLine("\x1b[31mteh\x1b[0m ok", 5)
	if len(got) != 2 {
		t.Fatalf("got %q", got)
	}
	if stripAnsi(got[0]) != "teh " || stripAnsi(got[1]) != "ok" {
		t.Fatalf("got %q", got)
	}
	if !strings.Contains(got[0], "\x1b[31mteh\x1b[0m") {
		t.Fatalf("style broken: %q", got[0])
	}
}

func TestWrapAnsiSplitWord(t *testing.T) {
	got := wrapLine("\x1b[31mabcdef\x1b[0m", 4)
	if len(got) != 2 || stripAnsi(got[0]) != "abcd" || stripAnsi(got[1]) != "ef" {
		t.Fatalf("got %q", got)
	}
	for _, row := range got {
		if strings.Count(row, "\x1b[31m") != strings.Count(row, "\x1b[0m") {
			t.Fatalf("unbalanced style in %q", row)
		}
	}
}

func TestWrapWideRunes(t *testing.T) {
	got := wrapLine("日本語ab", 4)
	if len(got) != 2 || stripAnsi(got[0]) != "日本" || stripAnsi(got[1]) != "語ab" {
		t.Fatalf("got %q", got)
	}
	for _, row := range got {
		if lipgloss.Width(row) > 4 {
			t.Fatalf("row too wide: %q", row)
		}
	}
}

func TestWrapPreservesSpaces(t *testing.T) {
	if got := wrapLine("a  b", 10); len(got) != 1 || got[0] != "a  b" {
		t.Fatalf("got %q", got)
	}
	if got := wrapLine("  hi", 10); len(got) != 1 || got[0] != "  hi" {
		t.Fatalf("indent lost: %q", got)
	}
}

func TestWrapTabExpands(t *testing.T) {
	got := wrapLine("a\tb", 10)
	if len(got) != 1 || got[0] != "a    b" {
		t.Fatalf("got %q", got)
	}
}

func TestWrapVisibleWidth(t *testing.T) {
	long := strings.Repeat("word ", 30)
	for _, row := range wrapLine(long, 20) {
		if lipgloss.Width(row) > 20 {
			t.Fatalf("row too wide: %q", row)
		}
	}
}

func TestViewWrapsTallDraft(t *testing.T) {
	b := newBrowser(testEntries(), "h", nil)
	b.draft = []rune(strings.Repeat("x", 200))
	m := teaModel{b: b, width: 60, height: 20}
	out := m.View()
	if n := strings.Count(out, "\n"); n != 19 {
		t.Fatalf("wrapped view has %d newlines, want 19", n)
	}
	if !strings.Contains(out, "-- new entry --") {
		t.Fatal("draft header missing")
	}
}

func TestViewCursorColumn(t *testing.T) {
	b := newBrowser(testEntries(), "h", nil)
	b.draft = []rune("hi")
	b.pos = 2
	m := teaModel{b: b, width: 60, height: 20}
	out := m.View()
	if !strings.Contains(out, "hi\x1b[7m \x1b[0m") {
		t.Fatalf("end cursor missing block:\n%s", out)
	}
	if strings.Contains(out, "\x1b[1A") {
		t.Fatal("view must not move the real cursor")
	}
	b.pos = 0
	m = teaModel{b: b, width: 60, height: 20}
	if out := m.View(); !strings.Contains(out, "\x1b[7mh\x1b[0mi") {
		t.Fatalf("start cursor wrong:\n%s", out)
	}
}

func TestViewCursorMultiline(t *testing.T) {
	b := newBrowser(testEntries(), "h", nil)
	b.draft = []rune("ab\ncd")
	b.pos = 4
	m := teaModel{b: b, width: 60, height: 20}
	if out := m.View(); !strings.Contains(out, "c\x1b[7md\x1b[0m") {
		t.Fatalf("second-line cursor wrong:\n%s", out)
	}
	b.draft = []rune("ab\ncd\nef")
	b.pos = 1
	m = teaModel{b: b, width: 60, height: 20}
	if out := m.View(); !strings.Contains(out, "a\x1b[7mb\x1b[0m") {
		t.Fatalf("upper-row cursor wrong:\n%s", out)
	}
}

var wellFormedEsc = regexp.MustCompile("\x1b\\[[0-9;]*m")

// TestViewNoDoubleMarkAfterNavigation reproduces the corrupted-frames
// report: browse a long entry with Up/Down, then the whole view must stay
// well-formed with every mark applied exactly once.
func TestViewNoDoubleMarkAfterNavigation(t *testing.T) {
	sc := testChecker()
	sc.customPath = ""
	long := "2026/10/16.. Ahh... Got a fucking sprain in my neck.. I can't lookup.. " +
		"I can't look down.. Tesla Motors just flew a goddam car FOR REAL. This is not " +
		"an AI SHIT.. Elon Musk just casually put a thrust vector control on a Roadster. " +
		"My jaw really hit the floor man.. This is what happens if SpaceX had a baby..LoL.. " +
		"THIS IS THE FUTURE! Dayyum. What a time to be alive."
	entries := []Entry{
		{Header: "2002..", Text: "2002.. first memory"},
		{Header: "2026/10/16..", Text: long},
		{Header: "2026/11/17..", Text: "2026/11/17.."},
	}
	m := teaModel{b: newBrowser(entries, "h", sc), width: 100, height: 24}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(teaModel)
	if m.b.topIdx != 1 {
		t.Fatalf("expected long entry focused, idx=%d", m.b.topIdx)
	}
	out := m.View()
	if strings.Contains(out, "\x1b[31\x1b[31m") {
		t.Fatalf("double-marked SGR after navigation:\n%s", out)
	}
	stripped := wellFormedEsc.ReplaceAllString(out, "")
	if strings.Contains(stripped, "\x1b") {
		t.Fatalf("malformed escape after navigation: %q", stripped)
	}
	assertAnsiBalanced(t, out)
	for _, word := range []string{"Motors", "goddam", "happens", "alive"} {
		if !strings.Contains(stripAnsi(out), word) {
			t.Fatalf("entry text lost: %q missing", word)
		}
	}
}

// TestViewNoDoubleMark is the regression test for corrupted frames: spell
// marks must be applied exactly once, so SGR bytes are never re-flagged as
// words (red "m" prefixes) and no escape is ever cut in half.
func TestViewNoDoubleMark(t *testing.T) {
	sc := testChecker()
	sc.customPath = ""
	b := newBrowser(testEntries(), "h", sc)
	typeText(b, "hello teh world")
	m := teaModel{b: b, width: 60, height: 20}
	out := m.View()
	if strings.Contains(out, "\x1b[31\x1b[31m") {
		t.Fatalf("double-marked SGR in view:\n%s", out)
	}
	stripped := wellFormedEsc.ReplaceAllString(out, "")
	if strings.Contains(stripped, "\x1b") {
		t.Fatalf("malformed escape in view: %q", stripped)
	}
}

// TestViewAnsiBalanced ensures every red-open has a reset in the view.
// assertAnsiBalanced fails the test if any escape is malformed or any red
// span is left open. Cursor codes (\x1b[7m) reset independently.
func assertAnsiBalanced(t *testing.T, out string) {
	t.Helper()
	depth := 0
	for i := 0; i < len(out); {
		switch {
		case strings.HasPrefix(out[i:], "\x1b[31m"):
			depth++
			i += len("\x1b[31m")
		case strings.HasPrefix(out[i:], "\x1b[0m"):
			if depth > 0 {
				depth--
			}
			i += len("\x1b[0m")
		case strings.HasPrefix(out[i:], "\x1b[7m"):
			i += len("\x1b[7m")
		case out[i] == 0x1b:
			t.Fatalf("unexpected escape at byte %d: %q", i, out[max(0, i-10):min(len(out), i+10)])
			i++
		default:
			i++
		}
	}
	if depth != 0 {
		t.Fatalf("unclosed red span, depth=%d", depth)
	}
}

func TestViewAnsiBalanced(t *testing.T) {
	sc := testChecker()
	sc.customPath = ""
	b := newBrowser(testEntries(), "h", sc)
	typeText(b, "chruch zqxq")
	m := teaModel{b: b, width: 60, height: 20}
	out := m.View()
	if !strings.Contains(out, "\x1b[31mchruch") {
		t.Fatal("expected marked typos")
	}
	assertAnsiBalanced(t, out)
}

// TestViewMarkedWordIntact ensures each misspelling is wrapped exactly once.
func TestViewMarkedWordIntact(t *testing.T) {
	sc := testChecker()
	sc.customPath = ""
	b := newBrowser(testEntries(), "h", sc)
	typeText(b, "teh")
	m := teaModel{b: b, width: 60, height: 20}
	out := m.View()
	if n := strings.Count(out, "\x1b[31mteh\x1b[0m"); n != 1 {
		t.Fatalf("expected one marked teh, got %d in:\n%s", n, out)
	}
}

// TestViewCounterUnmarked forces overflow so the fit counter appears, and
// ensures the counter line itself carries no highlight codes.
func TestViewCounterUnmarked(t *testing.T) {
	sc := testChecker()
	sc.customPath = ""
	entries := []Entry{{Header: "2001..", Text: "2001.. " + strings.Repeat("word ", 50)}}
	b := newBrowser(entries, "h", sc)
	m := teaModel{b: b, width: 40, height: 12}
	out := m.View()
	idx := strings.Index(out, "... (")
	if idx < 0 {
		t.Fatalf("expected fit counter in:\n%s", out)
	}
	line := out[idx:]
	if nl := strings.Index(line, "\n"); nl >= 0 {
		line = line[:nl]
	}
	if strings.Contains(line, "\x1b") {
		t.Fatalf("counter line carries ANSI: %q", line)
	}
}

func TestTruncAnsi(t *testing.T) {
	if got := truncAnsi("abcdef", 4, "…"); got != "abc…" {
		t.Fatalf("got %q", got)
	}
	if got := truncAnsi("abcdef", 10, "…"); got != "abcdef" {
		t.Fatalf("got %q", got)
	}
	marked := "\x1b[31mabcdef\x1b[0m rest"
	got := truncAnsi(marked, 4, "…")
	if stripAnsi(got) != "abc…" {
		t.Fatalf("got %q", got)
	}
	if strings.Count(got, "\x1b[31m") != 1 || strings.Count(got, "\x1b[0m") != 1 {
		t.Fatalf("broken style in %q", got)
	}
}
