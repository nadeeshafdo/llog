package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/term"
)

const (
	keyCtrlC     = 0x03
	keyBackspace = 0x08
	keyCtrlS     = 0x13
	keyEsc       = 0x1b
	keyDel       = 0x7f
)

// readInput reads an entry from in. On a terminal it runs the full-screen
// prompt with history (Up/Down), search (Ctrl+F) and editing (Ctrl+E);
// otherwise it falls back to the plain line reader so pipes keep working.
func readInput(in io.Reader, out io.Writer, entries []Entry, nowHeader string, checker *spellChecker) (editResult, error) {
	if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		if of, ok := out.(*os.File); ok && term.IsTerminal(int(of.Fd())) {
			return tuiLoop(f, of, entries, nowHeader, checker)
		}
		state, err := term.MakeRaw(int(f.Fd()))
		if err != nil {
			return editResult{editIdx: -1}, err
		}
		defer term.Restore(int(f.Fd()), state)
	}
	body, saved, err := editEntry(in, out)
	return editResult{body: body, saved: saved, editIdx: -1}, err
}

// editEntry returns the typed body and whether the user chose to save.
// Ctrl+S saves, Ctrl+C discards, and end of input saves whatever was read.
func editEntry(in io.Reader, out io.Writer) (string, bool, error) {
	r := bufio.NewReader(in)
	var body []rune
	for {
		ch, _, err := r.ReadRune()
		if errors.Is(err, io.EOF) {
			return string(body), true, nil
		}
		if err != nil {
			return "", false, err
		}

		switch {
		case ch == keyCtrlS:
			return string(body), true, nil
		case ch == keyCtrlC:
			return "", false, nil
		case ch == '\r' || ch == '\n':
			body = append(body, '\n')
			fmt.Fprint(out, "\r\n")
		case ch == keyBackspace || ch == keyDel:
			if n := len(body); n > 0 && body[n-1] != '\n' {
				body = body[:n-1]
				fmt.Fprint(out, "\b \b")
			}
		case ch == keyEsc:
			skipEscapeSequence(r)
		case ch == '\t' || (ch >= 0x20 && ch != keyDel):
			body = append(body, ch)
			fmt.Fprint(out, string(ch))
		}
	}
}

// skipEscapeSequence drops arrow keys and other ESC [ ... sequences.
func skipEscapeSequence(r *bufio.Reader) {
	next, _, err := r.ReadRune()
	if err != nil || (next != '[' && next != 'O') {
		return
	}
	for {
		c, _, err := r.ReadRune()
		if err != nil || (c >= 0x40 && c <= 0x7e) {
			return
		}
	}
}
