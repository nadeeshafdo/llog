package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testChecker() *spellChecker {
	sc := &spellChecker{
		words:       make(map[string]bool),
		autoCorrect: true,
		color:       true,
		enabled:     true,
	}
	for _, w := range []string{
		"hello", "world", "this", "is", "a", "test", "the", "church",
		"went", "apple", "apply", "banana", "quick", "brown", "fox",
		"do", "not", "mom", "can", "will",
	} {
		sc.words[w] = true
	}
	sc.rebuildIndex()
	return sc
}

func TestCheck(t *testing.T) {
	sc := testChecker()
	correct := []string{"hello", "Hello", "CHURCH", "SLT", "AIML", "2026", "4th", "21st", "don't", "mom's", "can't", "won't"}
	for _, w := range correct {
		if !sc.check(w) {
			t.Fatalf("%q should be accepted", w)
		}
	}
	wrong := []string{"teh", "chruch", "helo", "wrold"}
	for _, w := range wrong {
		if sc.check(w) {
			t.Fatalf("%q should be flagged", w)
		}
	}
}

func TestSuggest(t *testing.T) {
	sc := testChecker()
	got := sc.suggest("aple", 3)
	if len(got) == 0 || got[0] != "apple" {
		t.Fatalf("got %v, want apple first", got)
	}
	if got := sc.suggest("xyzqq", 3); len(got) != 0 {
		t.Fatalf("expected no suggestions, got %v", got)
	}
}

func TestFix(t *testing.T) {
	sc := testChecker()
	if rep, ok := sc.fix("teh"); !ok || rep != "the" {
		t.Fatalf("got %q,%v", rep, ok)
	}
	if rep, ok := sc.fix("chuch"); !ok || rep != "church" {
		t.Fatalf("got %q,%v", rep, ok)
	}
	// Correct words are never "fixed".
	if _, ok := sc.fix("hello"); ok {
		t.Fatal("correct word should not be fixed")
	}
	// Capitalized words (names) are never touched.
	if _, ok := sc.fix("Pesandu"); ok {
		t.Fatal("capitalized word should not be fixed")
	}
	// Short words are left alone.
	if _, ok := sc.fix("te"); ok {
		t.Fatal("short word should not be fixed")
	}
	sc.autoCorrect = false
	if _, ok := sc.fix("teh"); ok {
		t.Fatal("autocorrect off should not fix")
	}
}

func TestMark(t *testing.T) {
	sc := testChecker()
	got := sc.mark("hello teh world")
	if !strings.Contains(got, "\x1b[31mteh\x1b[0m") {
		t.Fatalf("misspelling not highlighted: %q", got)
	}
	if strings.Contains(got, "\x1b[31mhello") {
		t.Fatalf("correct word highlighted: %q", got)
	}
	sc.color = false
	if got := sc.mark("hello teh"); got != "hello teh" {
		t.Fatalf("color off should leave text plain: %q", got)
	}
}

func TestLevenshtein(t *testing.T) {
	if d := levenshteinCutoff([]rune("kitten"), []rune("sitting"), 10); d != 3 {
		t.Fatalf("got %d", d)
	}
	if d := levenshteinCutoff([]rune("abc"), []rune("xyz"), 2); d != 3 {
		t.Fatalf("cutoff should return 3, got %d", d)
	}
}

func TestLoadCheckerLearnsLogWords(t *testing.T) {
	entries := []Entry{
		{Header: "2002..", Text: "2002.. met zqxjk at the park"},
		{Header: "2003..", Text: "2003.. saw zqxjk again"},
		{Header: "2004..", Text: "2004.. once mentioned qwertyuiop"},
	}
	sc := loadChecker(entries)
	if !sc.check("zqxjk") {
		t.Fatal("repeated log word should be accepted")
	}
	sc.autoCorrect = true
	if _, ok := sc.fix("zqxjk"); ok {
		t.Fatal("known log word should not be fixed")
	}
}

func TestAddCustomPersists(t *testing.T) {
	dir := t.TempDir()
	sc := testChecker()
	sc.customPath = filepath.Join(dir, "words.txt")
	if err := sc.addCustom("Zqxjk"); err != nil {
		t.Fatal(err)
	}
	if !sc.check("zqxjk") {
		t.Fatal("added word should be accepted")
	}
	data, err := os.ReadFile(sc.customPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "zqxjk") {
		t.Fatalf("custom file missing word: %q", string(data))
	}
}
