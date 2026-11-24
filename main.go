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
  llog "text"          append a one-line entry (any text not starting with -)
  llog -- "text"       append text starting with - literally
  llog -h              show this help
  llog -v              show version

keys while writing:
  Type to write; Left/Right/Home/End move the cursor anywhere
  Enter newline, Backspace delete, Up/Down browse previous entries,
  Ctrl+F search the log, Ctrl+E edit the shown entry (there Up/Down
  moves the cursor instead, Esc cancels without saving),
  Tab fix word, Ctrl+A add word to dictionary, Ctrl+S save,
  Ctrl+C discard, Backspace right after an autocorrect undoes it

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
	if len(args) == 0 {
		return interactive(in, stderr)
	}
	// Only four flags exist: -h, --help, -v, --version. Anything starting
	// with - is a flag, so a typo can never silently become a journal
	// entry. Use -- to journal text that starts with a dash.
	if args[0] == "--" {
		if len(args) < 2 {
			return errors.New(`no entry text after "--" (see llog -h)`)
		}
		return save(newEntry(time.Now(), strings.Join(args[1:], " ")), stderr)
	}
	if strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "-h", "--help":
			if len(args) > 1 {
				return fmt.Errorf("flag %s takes no arguments (see llog -h)", args[0])
			}
			fmt.Fprint(stdout, usage)
			return nil
		case "-v", "--version":
			if len(args) > 1 {
				return fmt.Errorf("flag %s takes no arguments (see llog -h)", args[0])
			}
			fmt.Fprintln(stdout, versionString())
			return nil
		default:
			return fmt.Errorf("unknown flag %q (see llog -h)", args[0])
		}
	}
	return save(newEntry(time.Now(), strings.Join(args, " ")), stderr)
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
