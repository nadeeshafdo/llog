package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const backupTimeLayout = "20060102-150405.000000000"

func hashOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// withLogLock holds an exclusive lock while fn runs so two llog processes
// cannot write at the same time. The lock file is empty and stable across
// renames of the log file itself.
func withLogLock(logPath string, fn func() error) error {
	f, err := os.OpenFile(logPath+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("lock: %w", err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock: %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

// appendEntry backs up logPath into backupDir, then appends entry to the end
// of logPath. It never rewrites, truncates, or creates the log file.
func appendEntry(logPath, backupDir, entry string) (string, error) {
	return doAppend(logPath, backupDir, entry, "")
}

// doAppend is appendEntry with an optional fingerprint check: when expectHash
// is non-empty the write is refused if the file no longer matches it.
func doAppend(logPath, backupDir, entry, expectHash string) (string, error) {
	var backupPath string
	err := withLogLock(logPath, func() error {
		cur, err := os.ReadFile(logPath)
		if err != nil {
			return fmt.Errorf("cannot read log (is the card mounted?): %w", err)
		}
		if len(cur) == 0 {
			return errors.New("log file is empty, refusing to append")
		}
		if expectHash != "" && hashOf(cur) != expectHash {
			return errors.New("log changed on disk since you started (edited elsewhere?) - refusing to append to protect your log")
		}
		added := separator(cur) + entry + "\n"
		bp, err := backup(cur, backupDir)
		if err != nil {
			return err
		}
		backupPath = bp
		if err := writeSynced(logPath, []byte(added), os.O_WRONLY|os.O_APPEND); err != nil {
			return fmt.Errorf("append failed, original is in %s: %w", bp, err)
		}
		got, err := os.ReadFile(logPath)
		if err != nil {
			return fmt.Errorf("cannot re-read log after append, original is in %s: %w", bp, err)
		}
		if string(got) != string(cur)+added {
			return fmt.Errorf("verification failed after append, original is in %s", bp)
		}
		return nil
	})
	if err != nil {
		return backupPath, err
	}
	return backupPath, nil
}

// updateEntry replaces entries[index] with newText and writes the whole file
// back atomically. Every other byte must survive unchanged; otherwise the
// write is reported as failed and the backup path is returned.
func updateEntry(logPath, backupDir, expectHash string, index int, newText string) (string, error) {
	newText = strings.ReplaceAll(newText, "\r\n", "\n")
	newText = strings.ReplaceAll(newText, "\r", "")
	var backupPath string
	err := withLogLock(logPath, func() error {
		cur, err := os.ReadFile(logPath)
		if err != nil {
			return fmt.Errorf("cannot read log (is the card mounted?): %w", err)
		}
		if len(cur) == 0 {
			return errors.New("log file is empty, refusing to edit")
		}
		if expectHash == "" {
			return errors.New("refusing to edit without a fresh fingerprint")
		}
		if hashOf(cur) != expectHash {
			return errors.New("log changed on disk since you started (edited elsewhere?) - refusing to edit to protect your log")
		}
		crlf := bytes.Contains(cur, []byte("\r\n"))
		norm := strings.ReplaceAll(string(cur), "\r\n", "\n")
		entries := parseLog(norm)
		if index < 0 || index >= len(entries) {
			return fmt.Errorf("entry %d no longer exists", index+1)
		}
		texts := make([]string, len(entries))
		for i, e := range entries {
			texts[i] = e.Text
		}
		texts[index] = newText
		rebuilt := joinEntries(texts, crlf)
		bp, err := backup(cur, backupDir)
		if err != nil {
			return err
		}
		backupPath = bp
		if err := atomicWrite(logPath, []byte(rebuilt)); err != nil {
			return fmt.Errorf("edit failed, original is in %s: %w", bp, err)
		}
		got, err := os.ReadFile(logPath)
		if err != nil {
			return fmt.Errorf("cannot re-read log after edit, original is in %s: %w", bp, err)
		}
		back := parseLog(strings.ReplaceAll(string(got), "\r\n", "\n"))
		if len(back) != len(texts) {
			return fmt.Errorf("verification failed after edit, original is in %s", bp)
		}
		for i := range texts {
			if back[i].Text != texts[i] {
				return fmt.Errorf("verification failed after edit, original is in %s", bp)
			}
		}
		return nil
	})
	if err != nil {
		return backupPath, err
	}
	return backupPath, nil
}

func joinEntries(texts []string, crlf bool) string {
	s := strings.Join(texts, "\n")
	if crlf {
		s = strings.ReplaceAll(s, "\n", "\r\n")
	}
	return s
}

// atomicWrite replaces path with data via temp file + rename, synced.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".llog-tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
	return nil
}

func backup(data []byte, dir string) (string, error) {
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", fmt.Errorf("backup folder: %w", err)
	}
	path := filepath.Join(dir, "LOG.TXT."+time.Now().Format(backupTimeLayout)+".bak")
	if err := writeSynced(path, data, os.O_WRONLY|os.O_CREATE|os.O_EXCL); err != nil {
		return "", fmt.Errorf("backup: %w", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("cannot re-read backup %s: %w", path, err)
	}
	if !bytes.Equal(got, data) {
		return "", fmt.Errorf("backup %s does not match the original, aborting", path)
	}
	return path, nil
}

// separator returns the text needed so the new entry starts after a blank line.
func separator(orig []byte) string {
	switch {
	case bytes.HasSuffix(orig, []byte("\n\n")):
		return ""
	case bytes.HasSuffix(orig, []byte("\n")):
		return "\n"
	default:
		return "\n\n"
	}
}

func writeSynced(path string, data []byte, flag int) error {
	f, err := os.OpenFile(path, flag, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
