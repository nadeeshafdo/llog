package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var fixedNow = time.Date(2026, 11, 19, 18, 30, 0, 0, time.Local)

func TestVersionFlag(t *testing.T) {
	appVersion, appCommit, appDate = "v0.1.0-test", "abc1234", "2026-11-24"
	defer func() { appVersion, appCommit, appDate = "", "", "" }()
	for _, args := range [][]string{{"--version"}, {"-v"}} {
		dir := t.TempDir()
		logPath := filepath.Join(dir, "LOG.TXT")
		mustWrite(t, logPath, "start\n")
		t.Setenv("LLOG_FILE", logPath)
		t.Setenv("LLOG_BACKUP_DIR", filepath.Join(dir, "backups"))

		var stdout, stderr bytes.Buffer
		if err := run(args, strings.NewReader(""), &stdout, &stderr); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if stdout.String() != "llog v0.1.0-test (abc1234 2026-11-24)\n" {
			t.Fatalf("%v printed %q", args, stdout.String())
		}
		if got := mustRead(t, logPath); got != "start\n" {
			t.Fatalf("%v wrote to the log: %q", args, got)
		}
		if _, err := os.Stat(filepath.Join(dir, "backups")); !os.IsNotExist(err) {
			t.Fatalf("%v must not create backups", args)
		}
	}
}

func TestVersionStringFallback(t *testing.T) {
	defer func() { appVersion, appCommit, appDate = "", "", "" }()
	appVersion, appCommit, appDate = "", "", ""
	got := versionString()
	if !strings.HasPrefix(got, "llog ") || got == "llog " {
		t.Fatalf("fallback gave %q", got)
	}
	appVersion, appCommit, appDate = "", "deadbee", ""
	if got := versionString(); got != "llog dev (deadbee)" {
		t.Fatalf("commit-only gave %q", got)
	}
}

func TestHelpFlag(t *testing.T) {
	for _, args := range [][]string{{"-h"}, {"--help"}} {
		dir := t.TempDir()
		logPath := filepath.Join(dir, "LOG.TXT")
		mustWrite(t, logPath, "start\n")
		t.Setenv("LLOG_FILE", logPath)
		t.Setenv("LLOG_BACKUP_DIR", filepath.Join(dir, "backups"))

		var stdout, stderr bytes.Buffer
		if err := run(args, strings.NewReader(""), &stdout, &stderr); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if !strings.HasPrefix(stdout.String(), "usage:") {
			t.Fatalf("%v printed %q", args, stdout.String())
		}
		if got := mustRead(t, logPath); got != "start\n" {
			t.Fatalf("%v wrote to the log: %q", args, got)
		}
	}
}

func TestBareWordsJournal(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"help"}, "help\n"},
		{[]string{"version"}, "version\n"},
		{[]string{"went", "-v", "home"}, "went -v home\n"},
		{[]string{"--", "--version"}, "--version\n"},
		{[]string{"--", "-v", "x"}, "-v x\n"},
	} {
		dir := t.TempDir()
		logPath := filepath.Join(dir, "LOG.TXT")
		mustWrite(t, logPath, "start\n")
		t.Setenv("LLOG_FILE", logPath)
		t.Setenv("LLOG_BACKUP_DIR", filepath.Join(dir, "backups"))

		var stdout, stderr bytes.Buffer
		if err := run(tc.args, strings.NewReader(""), &stdout, &stderr); err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		if got := mustRead(t, logPath); !strings.HasSuffix(got, tc.want) {
			t.Fatalf("%v gave %q, want suffix %q", tc.args, got, tc.want)
		}
	}
}

func TestUnknownFlagRefused(t *testing.T) {
	for _, args := range [][]string{{"--foo"}, {"-x"}, {"--"}, {"-v", "extra"}, {"-h", "extra"}, {"--config", "extra"}, {"--update", "extra"}} {
		dir := t.TempDir()
		logPath := filepath.Join(dir, "LOG.TXT")
		mustWrite(t, logPath, "start\n")
		t.Setenv("LLOG_FILE", logPath)
		t.Setenv("LLOG_BACKUP_DIR", filepath.Join(dir, "backups"))

		var stdout, stderr bytes.Buffer
		if err := run(args, strings.NewReader(""), &stdout, &stderr); err == nil {
			t.Fatalf("%v should fail", args)
		}
		if got := mustRead(t, logPath); got != "start\n" {
			t.Fatalf("%v changed the log: %q", args, got)
		}
		if _, err := os.Stat(filepath.Join(dir, "backups")); !os.IsNotExist(err) {
			t.Fatalf("%v must not create backups", args)
		}
	}
}

func TestNewEntry(t *testing.T) {
	if got := newEntry(fixedNow, "hello"); got != "2026/11/19 18:30.. hello" {
		t.Fatalf("got %q", got)
	}
	if got := newEntry(fixedNow, "line one\r\nline two\n\n"); got != "2026/11/19 18:30.. line one\nline two" {
		t.Fatalf("got %q", got)
	}
	if got := newEntry(fixedNow, " \n\t\n"); got != "" {
		t.Fatalf("blank body should give empty entry, got %q", got)
	}
}

func TestAppendKeepsExistingBytesAndAddsNewlineWhenMissing(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "LOG.TXT")
	backupDir := filepath.Join(dir, "backups")
	orig := "2026/11/17.."
	mustWrite(t, logPath, orig)

	backupPath, err := appendEntry(logPath, backupDir, "2026/11/19 18:30.. new")
	if err != nil {
		t.Fatal(err)
	}
	got := mustRead(t, logPath)
	want := orig + "\n\n2026/11/19 18:30.. new\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if mustRead(t, backupPath) != orig {
		t.Fatalf("backup does not match original")
	}
}

func TestAppendAfterTrailingNewlineAddsBlankLine(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "LOG.TXT")
	mustWrite(t, logPath, "old\n")

	if _, err := appendEntry(logPath, filepath.Join(dir, "b"), "new"); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, logPath); got != "old\n\nnew\n" {
		t.Fatalf("got %q", got)
	}
}

func TestMissingLogIsNotCreated(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "LOG.TXT")

	if _, err := appendEntry(logPath, filepath.Join(dir, "b"), "new"); err == nil {
		t.Fatal("expected error for missing log")
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("log file must not be created, stat err=%v", err)
	}
}

func TestEmptyLogIsRefused(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "LOG.TXT")
	mustWrite(t, logPath, "")

	if _, err := appendEntry(logPath, filepath.Join(dir, "b"), "new"); err == nil {
		t.Fatal("expected error for empty log")
	}
	if got := mustRead(t, logPath); got != "" {
		t.Fatalf("empty log must stay empty, got %q", got)
	}
}

func TestRunOneLinerAndInteractive(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "LOG.TXT")
	mustWrite(t, logPath, "start\n")
	t.Setenv("LLOG_FILE", logPath)
	t.Setenv("LLOG_BACKUP_DIR", filepath.Join(dir, "backups"))

	var stdout, stderr bytes.Buffer
	if err := run([]string{"quick", "note"}, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(mustRead(t, logPath), "quick note\n") {
		t.Fatalf("one-liner not appended: %q", mustRead(t, logPath))
	}

	if err := run(nil, strings.NewReader("first line\nsecond line\n"), &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	got := mustRead(t, logPath)
	if !strings.Contains(got, "first line\nsecond line\n") {
		t.Fatalf("interactive entry not appended: %q", got)
	}
	if !strings.HasPrefix(got, "start\n") {
		t.Fatalf("existing content changed: %q", got)
	}

	entries, err := os.ReadDir(filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 backups, got %d", len(entries))
	}
}

func TestRunEmptyInteractiveWritesNothing(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "LOG.TXT")
	mustWrite(t, logPath, "start\n")
	t.Setenv("LLOG_FILE", logPath)
	t.Setenv("LLOG_BACKUP_DIR", filepath.Join(dir, "backups"))

	var stdout, stderr bytes.Buffer
	if err := run(nil, strings.NewReader("   \n\n"), &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, logPath); got != "start\n" {
		t.Fatalf("empty entry changed log: %q", got)
	}
}

func TestEditEntryKeys(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		body  string
		saved bool
	}{
		{"ctrl-s saves and ignores the rest", "hello\x13ignored", "hello", true},
		{"ctrl-c discards", "hello\x03", "", false},
		{"backspace removes a character", "helo\x7flo\x13", "hello", true},
		{"backspace does not cross a line break", "a\n\x7f\x7fb\x13", "a\nb", true},
		{"arrow keys are ignored", "ab\x1b[Dc\x13", "abc", true},
		{"carriage return is a newline", "x\ry\x13", "x\ny", true},
		{"end of input saves", "partial", "partial", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body, saved, err := editEntry(strings.NewReader(c.in), io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			if body != c.body || saved != c.saved {
				t.Fatalf("got body=%q saved=%v, want body=%q saved=%v", body, saved, c.body, c.saved)
			}
		})
	}
}

func TestRunCtrlCWritesNothing(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "LOG.TXT")
	mustWrite(t, logPath, "start\n")
	t.Setenv("LLOG_FILE", logPath)
	t.Setenv("LLOG_BACKUP_DIR", filepath.Join(dir, "backups"))

	var stdout, stderr bytes.Buffer
	if err := run(nil, strings.NewReader("draft\x03"), &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, logPath); got != "start\n" {
		t.Fatalf("discarded entry changed log: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "backups")); !os.IsNotExist(err) {
		t.Fatalf("discarded entry must not create backups, stat err=%v", err)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRunConfigWritesToken(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "LOG.TXT")
	mustWrite(t, logPath, "start\n")
	t.Setenv("LLOG_FILE", logPath)
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")

	var stdout, stderr bytes.Buffer
	if err := run([]string{"--config"}, strings.NewReader("tok123\n"), &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	authPath := filepath.Join(dir, "auth.json")
	if githubToken(authPath) != "tok123" {
		t.Fatalf("stored token not readable: %q", mustRead(t, authPath))
	}
	if got := mustRead(t, logPath); got != "start\n" {
		t.Fatalf("config must not touch the log: %q", got)
	}
}

func TestRunConfigEmptyAborts(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "LOG.TXT")
	mustWrite(t, logPath, "start\n")
	t.Setenv("LLOG_FILE", logPath)

	var stdout, stderr bytes.Buffer
	if err := run([]string{"--config"}, strings.NewReader("\n"), &stdout, &stderr); err == nil {
		t.Fatal("empty token should fail")
	}
	if _, err := os.Stat(filepath.Join(dir, "auth.json")); !os.IsNotExist(err) {
		t.Fatal("empty token must not create a file")
	}
}

func TestGithubTokenPrefersEnv(t *testing.T) {
	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(authPath, []byte(`{"github_token": "filetok"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := githubToken(authPath); got != "filetok" {
		t.Fatalf("file token = %q", got)
	}
	t.Setenv("GH_TOKEN", "envtok")
	if got := githubToken(authPath); got != "envtok" {
		t.Fatalf("env should win, got %q", got)
	}
	if got := githubToken(filepath.Join(dir, "missing.json")); got != "envtok" {
		t.Fatalf("missing file with env set = %q", got)
	}
}

func TestFileTokenRejectsGarbage(t *testing.T) {
	if got := fileToken(""); got != "" {
		t.Fatalf("empty path gave %q", got)
	}
	if got := fileToken(filepath.Join(t.TempDir(), "nope.json")); got != "" {
		t.Fatalf("missing file gave %q", got)
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(p, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := fileToken(p); got != "" {
		t.Fatalf("garbage gave %q", got)
	}
}
