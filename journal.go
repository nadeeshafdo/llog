package main

import (
	"os"
	"regexp"
	"strings"
)

// Entry is one dated block of the log. Header is the date prefix
// (for example "2026/11/19 18:30..") or "" for the leading preamble.
// Text is the full raw text of the block.
type Entry struct {
	Header string
	Text   string
}

var datePrefix = regexp.MustCompile(`^(\d{4}(?:/\d{2}/\d{2}(?: \d{2}:\d{2})?)?\.\.)`)

// parseLog splits the log into entries. Every line starting with a date
// prefix begins a new entry; all other lines (banners, blank lines,
// continuations) belong to the current entry.
func parseLog(data string) []Entry {
	data = strings.ReplaceAll(data, "\r\n", "\n")
	lines := strings.Split(data, "\n")
	var entries []Entry
	var cur *Entry
	for _, line := range lines {
		if m := datePrefix.FindString(line); m != "" {
			entries = append(entries, Entry{Header: m, Text: line})
			cur = &entries[len(entries)-1]
			continue
		}
		if cur == nil {
			entries = append(entries, Entry{Header: "", Text: line})
			cur = &entries[len(entries)-1]
			continue
		}
		cur.Text += "\n" + line
	}
	return entries
}

func loadEntries(logPath string) ([]Entry, error) {
	data, err := os.ReadFile(logPath)
	if err != nil {
		return nil, err
	}
	return parseLog(string(data)), nil
}

// entryBody returns the editable text of an entry: everything after the
// header prefix (and its following spaces) on the first line.
func entryBody(e Entry) string {
	if e.Header == "" {
		return e.Text
	}
	rest := strings.TrimPrefix(e.Text, e.Header)
	if i := strings.Index(rest, "\n"); i >= 0 {
		return strings.TrimLeft(rest[:i], " \t") + rest[i:]
	}
	return strings.TrimLeft(rest, " \t")
}

// editedText rebuilds an entry's full text from its header and a new body,
// preserving the original spacing after the header.
func editedText(e Entry, body string) string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\r", "")
	body = strings.Trim(body, " \t\n")
	if e.Header == "" {
		return body
	}
	sep := " "
	rest := strings.TrimPrefix(e.Text, e.Header)
	for i := 0; i < len(rest); i++ {
		if rest[i] != ' ' && rest[i] != '\t' {
			sep = rest[:i]
			break
		}
		if i == len(rest)-1 {
			sep = rest
		}
	}
	if sep == "" {
		sep = " "
	}
	return e.Header + sep + body
}

// searchEntries returns the indexes of entries containing query,
// case-insensitively, in file order (oldest first).
func searchEntries(entries []Entry, query string) []int {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil
	}
	var out []int
	for i, e := range entries {
		if strings.Contains(strings.ToLower(e.Text), q) {
			out = append(out, i)
		}
	}
	return out
}
