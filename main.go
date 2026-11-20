package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

const (
	defaultLogPath   = "/media/nadeeshafdo/nadeeshafdo/LOG.TXT"
	defaultBackupDir = "/media/nadeeshafdo/nadeeshafdo/llog-backups"
)

const usage = `usage:
  llog                 write a multi-line entry (Ctrl+S saves, Ctrl+C discards)
  llog "text"          append a one-line entry
  llog help            show this help

keys while writing:
  Enter newline, Backspace delete, Up/Down browse previous entries,
  Ctrl+F search the log, Ctrl+E edit the shown entry, Tab fix word,
  Ctrl+A add word to dictionary, Ctrl+S save, Ctrl+C discard,
  Esc cancel search/edit, Backspace right after an autocorrect undoes it

environment:
  LLOG_FILE            path to LOG.TXT (default ` + defaultLogPath + `)
  LLOG_BACKUP_DIR      backup folder (default ` + defaultBackupDir + `)
  LLOG_DICT            spell dictionary path (default system hunspell)
  LLOG_AUTOCORRECT=0   disable autocorrect (spell underline stays)
`

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "llog:", err)
		os.Exit(1)
	}
}

func run(args []string, in io.Reader, stdout, stderr io.Writer) error {
	switch {
	case len(args) == 0:
		return interactive(in, stderr)
	case len(args) == 1 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help"):
		fmt.Fprint(stdout, usage)
		return nil
	default:
		return save(newEntry(time.Now(), strings.Join(args, " ")), stderr)
	}
}

func interactive(in io.Reader, out io.Writer) error {
	now := time.Now()
	logPath := envOr("LLOG_FILE", defaultLogPath)
	backupDir := envOr("LLOG_BACKUP_DIR", defaultBackupDir)
	data, err := os.ReadFile(logPath)
	if err != nil {
		return fmt.Errorf("cannot read log (is the card mounted?): %w", err)
	}
	if len(data) == 0 {
		return errors.New("log file is empty, refusing to write")
	}
	fingerprint := hashOf(data)
	entries := parseLog(string(data))
	checker := loadChecker(entries)

	res, err := readInput(in, out, entries, header(now), checker)
	if err != nil {
		return err
	}
	if !res.saved {
		fmt.Fprintln(out, "discarded")
		return nil
	}
	if res.editIdx >= 0 {
		return saveEdit(logPath, backupDir, fingerprint, entries, res.editIdx, res.body, out)
	}
	return saveNew(logPath, backupDir, fingerprint, data, now, res.body, out)
}

func saveNew(logPath, backupDir, fingerprint string, data []byte, now time.Time, body string, out io.Writer) error {
	entry := newEntry(now, body)
	if entry == "" {
		fmt.Fprintln(out, "nothing written, entry was empty")
		return nil
	}
	backupPath, err := doAppend(logPath, backupDir, entry, fingerprint)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "saved to %s (backup: %s)\n", logPath, backupPath)
	return nil
}

func saveEdit(logPath, backupDir, fingerprint string, entries []Entry, idx int, body string, out io.Writer) error {
	if idx < 0 || idx >= len(entries) {
		return fmt.Errorf("entry %d no longer exists", idx+1)
	}
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.Trim(body, " \t\n")
	if body == "" {
		fmt.Fprintln(out, "edit is empty, cancelled (nothing written)")
		return nil
	}
	newText := editedText(entries[idx], body)
	if newText == entries[idx].Text {
		fmt.Fprintln(out, "no changes, nothing written")
		return nil
	}
	backupPath, err := updateEntry(logPath, backupDir, fingerprint, idx, newText)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "edited %s (backup: %s)\n", displayHeader(entries[idx].Header), backupPath)
	return nil
}

func save(entry string, out io.Writer) error {
	if entry == "" {
		fmt.Fprintln(out, "nothing written, entry was empty")
		return nil
	}
	logPath := envOr("LLOG_FILE", defaultLogPath)
	backupDir := envOr("LLOG_BACKUP_DIR", defaultBackupDir)

	backupPath, err := appendEntry(logPath, backupDir, entry)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "saved to %s (backup: %s)\n", logPath, backupPath)
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
