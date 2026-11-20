package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testLogContent() string {
	return strings.Join([]string{
		"2002.. first memory",
		"continued line",
		"",
		"2026/05/03.. went to the church",
		"",
		"2026/11/17..",
	}, "\n")
}

func TestUpdateEntryPreservesOthers(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "LOG.TXT")
	mustWrite(t, logPath, testLogContent())
	backupDir := filepath.Join(dir, "backups")
	data, _ := os.ReadFile(logPath)
	fp := hashOf(data)

	bp, err := updateEntry(logPath, backupDir, fp, 1, "2026/05/03.. went to the mosque")
	if err != nil {
		t.Fatal(err)
	}
	got := mustRead(t, logPath)
	if !strings.Contains(got, "2002.. first memory\ncontinued line") {
		t.Fatalf("first entry changed:\n%s", got)
	}
	if !strings.Contains(got, "2026/05/03.. went to the mosque") {
		t.Fatalf("edit missing:\n%s", got)
	}
	if !strings.HasSuffix(got, "2026/11/17..") {
		t.Fatalf("last entry changed:\n%s", got)
	}
	if mustRead(t, bp) != testLogContent() {
		t.Fatal("backup does not match original")
	}
}

func TestUpdateEntryFingerprintMismatch(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "LOG.TXT")
	mustWrite(t, logPath, testLogContent())

	if _, err := updateEntry(logPath, filepath.Join(dir, "b"), "deadbeef", 0, "x"); err == nil {
		t.Fatal("expected fingerprint error")
	}
	if got := mustRead(t, logPath); got != testLogContent() {
		t.Fatal("file must be unchanged after refused edit")
	}
	if _, err := os.Stat(filepath.Join(dir, "b")); !os.IsNotExist(err) {
		t.Fatal("refused edit must not create backups")
	}
}

func TestUpdateEntryRequiresFingerprint(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "LOG.TXT")
	mustWrite(t, logPath, testLogContent())

	if _, err := updateEntry(logPath, filepath.Join(dir, "b"), "", 0, "x"); err == nil {
		t.Fatal("expected error without fingerprint")
	}
}

func TestDoAppendFingerprintMismatch(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "LOG.TXT")
	mustWrite(t, logPath, "old\n")

	if _, err := doAppend(logPath, filepath.Join(dir, "b"), "new", "deadbeef"); err == nil {
		t.Fatal("expected fingerprint error")
	}
	if got := mustRead(t, logPath); got != "old\n" {
		t.Fatal("file must be unchanged after refused append")
	}
}

func TestParseRoundTrip(t *testing.T) {
	for _, orig := range []string{
		testLogContent(),
		"preamble\n\n2002.. x\n",
		"2002.. only",
		"2002.. a\r\ncontinued\r\n\r\n2003.. b\r\n",
	} {
		norm := strings.ReplaceAll(orig, "\r\n", "\n")
		entries := parseLog(norm)
		var texts []string
		for _, e := range entries {
			texts = append(texts, e.Text)
		}
		crlf := strings.Contains(orig, "\r\n")
		if got := joinEntries(texts, crlf); got != orig {
			t.Fatalf("round trip failed:\norig %q\ngot  %q", orig, got)
		}
	}
}

func TestEntryBody(t *testing.T) {
	e := Entry{Header: "2026/05/03..", Text: "2026/05/03.. went to the church\nsecond line"}
	if got := entryBody(e); got != "went to the church\nsecond line" {
		t.Fatalf("got %q", got)
	}
	e = Entry{Header: "2014..", Text: "2014.."}
	if got := entryBody(e); got != "" {
		t.Fatalf("got %q", got)
	}
	e = Entry{Header: "", Text: "banner\npreamble"}
	if got := entryBody(e); got != "banner\npreamble" {
		t.Fatalf("got %q", got)
	}
}

func TestEditedText(t *testing.T) {
	e := Entry{Header: "2026/05/03..", Text: "2026/05/03.. old body\nmore"}
	if got := editedText(e, "new body"); got != "2026/05/03.. new body" {
		t.Fatalf("got %q", got)
	}
	e = Entry{Header: "2014..", Text: "2014.."}
	if got := editedText(e, "now I remember"); got != "2014.. now I remember" {
		t.Fatalf("got %q", got)
	}
	e = Entry{Header: "", Text: "banner\nold"}
	if got := editedText(e, "banner\nnew"); got != "banner\nnew" {
		t.Fatalf("got %q", got)
	}
}

func TestSaveEditFlow(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "LOG.TXT")
	mustWrite(t, logPath, testLogContent())
	t.Setenv("LLOG_FILE", logPath)
	t.Setenv("LLOG_BACKUP_DIR", filepath.Join(dir, "backups"))
	data, _ := os.ReadFile(logPath)
	entries := parseLog(string(data))

	var out strings.Builder
	if err := saveEdit(logPath, filepath.Join(dir, "backups"), hashOf(data), entries, 1, "went to the mosque", &out); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, logPath); !strings.Contains(got, "went to the mosque") {
		t.Fatalf("edit not applied:\n%s", got)
	}

	out.Reset()
	if err := saveEdit(logPath, filepath.Join(dir, "backups"), hashOf(mustReadBytes(t, logPath)), parseLog(mustRead(t, logPath)), 1, "   ", &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "cancelled") {
		t.Fatalf("empty edit should cancel, got %q", out.String())
	}
}

func mustReadBytes(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
