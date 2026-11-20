package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLog(t *testing.T) {
	data := strings.Join([]string{
		"==================",
		"== PURPOSE...",
		"==================",
		"preamble text",
		"2002.. first memory",
		"continuation of 2002",
		"",
		"2026/11/19 18:30.. hello",
		"multi",
		"line",
	}, "\n")
	entries := parseLog(data)
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	if entries[0].Header != "" {
		t.Fatalf("preamble header should be empty, got %q", entries[0].Header)
	}
	if !strings.Contains(entries[0].Text, "preamble text") {
		t.Fatalf("preamble lost content: %q", entries[0].Text)
	}
	if entries[1].Header != "2002.." {
		t.Fatalf("got header %q", entries[1].Header)
	}
	if !strings.Contains(entries[1].Text, "continuation of 2002") {
		t.Fatalf("continuation lost: %q", entries[1].Text)
	}
	if entries[2].Header != "2026/11/19 18:30.." {
		t.Fatalf("got header %q", entries[2].Header)
	}
	if !strings.Contains(entries[2].Text, "multi\nline") {
		t.Fatalf("multiline lost: %q", entries[2].Text)
	}
}

func TestParseLogCRLF(t *testing.T) {
	entries := parseLog("2002.. one\r\ncontinued\r\n2026/11/19 18:30.. two\r\n")
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if strings.Contains(entries[0].Text, "\r") {
		t.Fatalf("CR not normalized: %q", entries[0].Text)
	}
}

func TestSearchEntries(t *testing.T) {
	entries := []Entry{
		{Header: "2002..", Text: "2002.. first memory about Nursery"},
		{Header: "2026/05/03..", Text: "2026/05/03.. went to the church"},
		{Header: "2026/11/19 18:30..", Text: "2026/11/19 18:30.. CHURCH again"},
	}
	got := searchEntries(entries, "church")
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("got %v", got)
	}
	if got := searchEntries(entries, "missing"); len(got) != 0 {
		t.Fatalf("expected no matches, got %v", got)
	}
	if got := searchEntries(entries, "   "); len(got) != 0 {
		t.Fatalf("blank query should give no matches, got %v", got)
	}
}

func TestLoadEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "LOG.TXT")
	mustWrite(t, path, "2002.. hello\n\n2003.. world\n")
	entries, err := loadEntries(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Header != "2002.." {
		t.Fatalf("got %+v", entries)
	}
}
