package main

import (
	"strings"
	"testing"

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
	if stripAnsi(got[0]) != "teh" || stripAnsi(got[1]) != "ok" {
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

func TestWrapCollapsesSpaces(t *testing.T) {
	if got := wrapLine("a  b", 10); len(got) != 1 || got[0] != "a b" {
		t.Fatalf("got %q", got)
	}
}

func TestWrapTabExpands(t *testing.T) {
	got := wrapLine("a\tb", 10)
	if len(got) != 1 || got[0] != "a\u00a0\u00a0\u00a0\u00a0b" {
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
	m := teaModel{b: b, width: 60, height: 20}
	out := m.View()
	if !strings.HasSuffix(out, "\x1b[1A\x1b[3G") {
		t.Fatalf("cursor suffix wrong: %q", out[len(out)-12:])
	}
}
