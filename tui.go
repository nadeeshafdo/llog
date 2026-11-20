package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

type keyKind int

const (
	kRune keyKind = iota
	kEnter
	kBackspace
	kUp
	kDown
	kLeft
	kRight
	kTab
	kCtrlA
	kCtrlC
	kCtrlE
	kCtrlS
	kCtrlF
	kEsc
	kUnknown
)

type key struct {
	kind keyKind
	r    rune
}

const (
	keyCtrlA = 0x01
	keyCtrlD = 0x04
	keyCtrlE = 0x05
	keyCtrlF = 0x06
	keyTab   = 0x09
)

// readKey reads one key press. Arrow keys arrive as escape sequences.
func readKey(r *bufio.Reader, f *os.File) (key, error) {
	ch, _, err := r.ReadRune()
	if err != nil {
		return key{}, err
	}
	switch ch {
	case keyCtrlA:
		return key{kind: kCtrlA}, nil
	case keyCtrlC:
		return key{kind: kCtrlC}, nil
	case keyCtrlE:
		return key{kind: kCtrlE}, nil
	case keyCtrlS, keyCtrlD:
		return key{kind: kCtrlS}, nil
	case keyCtrlF:
		return key{kind: kCtrlF}, nil
	case '\r', '\n':
		return key{kind: kEnter}, nil
	case keyBackspace, keyDel:
		return key{kind: kBackspace}, nil
	case keyTab:
		return key{kind: kTab}, nil
	case keyEsc:
		return readEscKey(r, f)
	case 0x20:
		return key{kind: kRune, r: ch}, nil
	default:
		if ch > 0x20 && ch != keyDel {
			return key{kind: kRune, r: ch}, nil
		}
		return key{kind: kUnknown}, nil
	}
}

// readEscKey parses an escape sequence. A lone Esc returns kEsc.
// It uses poll() with a short timeout to tell the two apart: sequence bytes
// arrive together with Esc, while a lone Esc has nothing after it. Unlike a
// read deadline, poll() cannot hang the session if it is unsupported.
func readEscKey(r *bufio.Reader, f *os.File) (key, error) {
	if r.Buffered() == 0 && !escSequenceWaiting(f) {
		return key{kind: kEsc}, nil
	}
	return readEscSequence(r)
}

// escSequenceWaiting reports whether more input arrives within a short
// window after an Esc byte.
func escSequenceWaiting(f *os.File) bool {
	pfds := []unix.PollFd{{Fd: int32(f.Fd()), Events: unix.POLLIN}}
	for {
		n, err := unix.Poll(pfds, 30)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return false
		}
		return n > 0
	}
}

func readEscSequence(r *bufio.Reader) (key, error) {
	next, _, err := r.ReadRune()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return key{kind: kEsc}, nil
		}
		return key{}, err
	}
	return readEscRest(r, next)
}

func readEscRest(r *bufio.Reader, next rune) (key, error) {
	if next != '[' && next != 'O' {
		return key{kind: kUnknown}, nil
	}
	for {
		c, _, err := r.ReadRune()
		if err != nil {
			return key{}, err
		}
		switch c {
		case 'A':
			return key{kind: kUp}, nil
		case 'B':
			return key{kind: kDown}, nil
		case 'C':
			return key{kind: kRight}, nil
		case 'D':
			return key{kind: kLeft}, nil
		default:
			if c >= 0x40 && c <= 0x7e {
				return key{kind: kUnknown}, nil
			}
		}
	}
}

type action int

const (
	actNone action = iota
	actSave
	actDiscard
)

// editResult is the outcome of the prompt. editIdx is -1 for a new entry,
// otherwise the index of the edited entry.
type editResult struct {
	body    string
	saved   bool
	editIdx int
}

type undoState struct {
	active bool
	snap   []rune
}

// browser holds the state of the writing prompt. The draft being typed is
// always kept; Up/Down only moves the history view, so browsing never loses
// what was typed.
type browser struct {
	entries   []Entry
	topIdx    int
	draft     []rune
	inPrompt  bool
	query     []rune
	inSearch  bool
	matches   []int
	midx      int
	nowHeader string
	checker   *spellChecker
	editing   bool
	editIdx   int
	stash     []rune
	status    string
	undo      undoState
}

func newBrowser(entries []Entry, nowHeader string, checker *spellChecker) *browser {
	return &browser{entries: entries, topIdx: len(entries) - 1, nowHeader: nowHeader, checker: checker}
}

func (b *browser) handle(k key) action {
	if k.kind != kBackspace {
		b.undo.active = false
	}
	b.status = ""
	if b.inPrompt {
		return b.handlePrompt(k)
	}
	if b.inSearch {
		return b.handleSearchView(k)
	}
	return b.handleEdit(k)
}

func (b *browser) handleEdit(k key) action {
	switch k.kind {
	case kCtrlC:
		if b.editing {
			b.cancelEdit("edit cancelled, nothing saved")
			return actNone
		}
		return actDiscard
	case kEsc:
		if b.editing {
			b.cancelEdit("edit cancelled, nothing saved")
		}
		return actNone
	case kCtrlS:
		return actSave
	case kCtrlE:
		b.startEdit()
		return actNone
	case kCtrlF:
		if b.editing {
			b.status = "finish the edit first (Ctrl+S / Esc)"
			return actNone
		}
		b.inPrompt = true
		b.query = nil
		return actNone
	case kCtrlA:
		b.addWord()
		return actNone
	case kUp, kDown:
		if b.editing {
			b.status = "navigation locked while editing (Esc to stop)"
			return actNone
		}
		if len(b.entries) > 0 {
			if k.kind == kUp && b.topIdx > 0 {
				b.topIdx--
			}
			if k.kind == kDown && b.topIdx < len(b.entries)-1 {
				b.topIdx++
			}
		}
		return actNone
	case kEnter:
		b.maybeAutocorrect()
		b.draft = append(b.draft, '\n')
		return actNone
	case kBackspace:
		if b.undo.active {
			b.draft = b.undo.snap
			b.undo.active = false
			b.status = "autocorrect undone"
			return actNone
		}
		if n := len(b.draft); n > 0 {
			b.draft = b.draft[:n-1]
		}
		return actNone
	case kTab:
		if !b.acceptSuggestion() {
			b.draft = append(b.draft, '\t')
		}
		return actNone
	case kRune:
		if isBoundary(k.r) {
			b.maybeAutocorrect()
		}
		b.draft = append(b.draft, k.r)
		return actNone
	default:
		return actNone
	}
}

func (b *browser) handlePrompt(k key) action {
	switch k.kind {
	case kEsc, kCtrlC:
		b.inPrompt = false
		b.query = nil
		return actNone
	case kEnter:
		ms := searchEntries(b.entries, string(b.query))
		if len(ms) == 0 {
			return actNone
		}
		b.matches = ms
		b.midx = len(ms) - 1
		b.inPrompt = false
		b.inSearch = true
		return actNone
	case kBackspace:
		if n := len(b.query); n > 0 {
			b.query = b.query[:n-1]
		}
		return actNone
	case kRune, kTab:
		r := k.r
		if k.kind == kTab {
			r = '\t'
		}
		b.query = append(b.query, r)
		return actNone
	default:
		return actNone
	}
}

func (b *browser) handleSearchView(k key) action {
	switch k.kind {
	case kEsc:
		b.inSearch = false
		b.matches = nil
		return actNone
	case kCtrlC:
		return actDiscard
	case kCtrlS:
		return actSave
	case kCtrlF:
		b.inSearch = false
		b.matches = nil
		b.inPrompt = true
		b.query = nil
		return actNone
	case kUp:
		if len(b.matches) > 0 {
			b.midx = (b.midx - 1 + len(b.matches)) % len(b.matches)
		}
		return actNone
	case kDown:
		if len(b.matches) > 0 {
			b.midx = (b.midx + 1) % len(b.matches)
		}
		return actNone
	default:
		return actNone
	}
}

func (b *browser) startEdit() {
	if b.editing {
		return
	}
	if len(b.entries) == 0 {
		b.status = "nothing to edit yet"
		return
	}
	b.stash = append([]rune(nil), b.draft...)
	b.editIdx = b.topIdx
	b.draft = []rune(strings.TrimRight(entryBody(b.entries[b.editIdx]), "\n"))
	b.editing = true
	b.status = "editing " + displayHeader(b.entries[b.editIdx].Header)
}

func (b *browser) cancelEdit(msg string) {
	b.draft = b.stash
	b.stash = nil
	b.editing = false
	b.status = msg
}

// maybeAutocorrect fixes the word before the cursor when it is a confident
// typo. The state before the fix is kept so Backspace undoes it.
func (b *browser) maybeAutocorrect() {
	if b.checker == nil || !b.checker.enabled {
		return
	}
	word, start := trailingWord(b.draft)
	if word == "" {
		return
	}
	rep, ok := b.checker.fix(word)
	if !ok {
		return
	}
	b.undo.snap = append([]rune(nil), b.draft...)
	b.undo.active = true
	b.draft = append(b.draft[:start], []rune(rep)...)
	b.status = "autocorrected " + word + " -> " + rep + " (Backspace undoes)"
}

// acceptSuggestion replaces the word before the cursor with the top
// suggestion. It reports whether a replacement happened.
func (b *browser) acceptSuggestion() bool {
	if b.checker == nil || !b.checker.enabled {
		return false
	}
	word, start := trailingWord(b.draft)
	if word == "" || b.checker.check(word) {
		return false
	}
	sugs := b.checker.suggest(word, 1)
	if len(sugs) == 0 {
		b.status = "no suggestion for " + word
		return false
	}
	b.undo.snap = append([]rune(nil), b.draft...)
	b.undo.active = true
	b.draft = append(b.draft[:start], []rune(sugs[0])...)
	b.status = "tab: " + word + " -> " + sugs[0] + " (Backspace undoes)"
	return true
}

func (b *browser) addWord() {
	if b.checker == nil || !b.checker.enabled {
		b.status = "spell check is off"
		return
	}
	word, _ := trailingWord(b.draft)
	if normalizeWord(word) == "" {
		b.status = "no word to add (move after a word)"
		return
	}
	if err := b.checker.addCustom(word); err != nil {
		b.status = "kept " + word + " for now (could not save: " + err.Error() + ")"
		return
	}
	b.status = "added " + normalizeWord(word) + " to dictionary"
}

// topPane returns the header line and body lines for the history/search view.
func (b *browser) topPane() (string, []string) {
	if b.inPrompt {
		q := string(b.query)
		ms := searchEntries(b.entries, q)
		head := fmt.Sprintf("-- search: %s (%d matches, Enter to browse, Esc cancel)", q, len(ms))
		if len(ms) == 0 {
			return head, []string{"(no matches)"}
		}
		return head, strings.Split(b.entries[ms[len(ms)-1]].Text, "\n")
	}
	if b.inSearch && len(b.matches) > 0 {
		e := b.entries[b.matches[b.midx]]
		head := fmt.Sprintf("[search %d/%d] %s", b.midx+1, len(b.matches), displayHeader(e.Header))
		return head, strings.Split(e.Text, "\n")
	}
	if len(b.entries) == 0 {
		return "[no previous entries]", []string{"(the log has no readable entries yet)"}
	}
	idx := b.topIdx
	if b.editing {
		idx = b.editIdx
	}
	e := b.entries[idx]
	verb := "history"
	if b.editing {
		verb = "editing"
	}
	head := fmt.Sprintf("[%s %d/%d] %s", verb, idx+1, len(b.entries), displayHeader(e.Header))
	return head, strings.Split(e.Text, "\n")
}

// bottomPane returns the header line and content lines for the input area.
func (b *browser) bottomPane() (string, []string) {
	head := ""
	var lines []string
	switch {
	case b.inPrompt:
		head = "-- type query, Enter to browse --"
		lines = []string{"search: " + string(b.query)}
	case b.inSearch:
		head = "-- new entry (locked: Esc to write, Up/Down moves matches) --"
		lines = splitDraft(b.draft)
	case b.editing:
		head = "-- editing old entry (Ctrl+S saves, Esc cancels WITHOUT saving) --"
		lines = splitDraft(b.draft)
	default:
		head = "-- new entry --"
		lines = splitDraft(b.draft)
	}
	if b.status != "" {
		head += " | " + b.status
	}
	return head, lines
}

func splitDraft(d []rune) []string {
	if len(d) == 0 {
		return []string{""}
	}
	return strings.Split(string(d), "\n")
}

func displayHeader(h string) string {
	if h == "" {
		return "(start of log)"
	}
	return h
}

func truncLine(s string, w int) string {
	if w <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	if w == 1 {
		return "…"
	}
	return string(r[:w-1]) + "…"
}

// fitLines truncates lines to the width and keeps at most maxRows of them,
// replacing the last row with a counter when content is cut.
func fitLines(lines []string, maxRows, w int) []string {
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		out = append(out, truncLine(ln, w))
	}
	if len(out) <= maxRows {
		return out
	}
	if maxRows <= 1 {
		return []string{fmt.Sprintf("... (%d more lines)", len(out))}
	}
	kept := out[:maxRows-1]
	return append(kept, fmt.Sprintf("... (%d more lines)", len(out)-(maxRows-1)))
}

func render(out io.Writer, b *browser, w, h int) {
	if w < 20 {
		w = 20
	}
	if h < 10 {
		h = 10
	}
	var sb strings.Builder
	sb.WriteString("\x1b[2J\x1b[H\x1b[?25l")
	spellTag := "spell off"
	if b.checker != nil && b.checker.enabled {
		spellTag = "spell on"
	}
	title := fmt.Sprintf("llog  %s  |  Ctrl+S save  Ctrl+C discard  Up/Down history  Ctrl+F search  Ctrl+E edit  Tab fix  |  %s", b.nowHeader, spellTag)
	sb.WriteString(truncLine(title, w) + "\r\n")
	sb.WriteString(strings.Repeat("-", w) + "\r\n")
	topHead, topBody := b.topPane()
	sb.WriteString(truncLine(topHead, w) + "\r\n")
	bottomHead, bottomLines := b.bottomPane()
	bottomRows := len(bottomLines)
	if bottomRows < 1 {
		bottomRows = 1
	}
	maxBody := h - 2 - 1 - 1 - 1 - bottomRows - 1
	if maxBody < 1 {
		maxBody = 1
	}
	for _, ln := range fitLines(topBody, maxBody, w) {
		sb.WriteString(markLine(b.checker, ln) + "\r\n")
	}
	sb.WriteString(strings.Repeat("-", w) + "\r\n")
	sb.WriteString(truncLine(bottomHead, w) + "\r\n")
	for i, ln := range bottomLines {
		if i == len(bottomLines)-1 {
			sb.WriteString(markLine(b.checker, ln))
			continue
		}
		sb.WriteString(markLine(b.checker, ln) + "\r\n")
	}
	sb.WriteString("\x1b[?25h")
	fmt.Fprint(out, sb.String())
}

func markLine(checker *spellChecker, line string) string {
	if checker == nil {
		return line
	}
	return checker.mark(line)
}

// tuiLoop runs the full-screen writing prompt.
func tuiLoop(inFile, outFile *os.File, entries []Entry, nowHeader string, checker *spellChecker) (editResult, error) {
	state, err := term.MakeRaw(int(inFile.Fd()))
	if err != nil {
		return editResult{editIdx: -1}, err
	}
	defer term.Restore(int(inFile.Fd()), state)
	fmt.Fprint(outFile, "\x1b[?1049h")
	defer fmt.Fprint(outFile, "\x1b[?1049l")

	b := newBrowser(entries, nowHeader, checker)
	r := bufio.NewReader(inFile)
	for {
		w, h, err := term.GetSize(int(outFile.Fd()))
		if err != nil {
			w, h = 80, 24
		}
		render(outFile, b, w, h)
		k, err := readKey(r, inFile)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return editResult{body: string(b.draft), saved: true, editIdx: editIdxOf(b)}, nil
			}
			return editResult{editIdx: -1}, err
		}
		switch b.handle(k) {
		case actSave:
			return editResult{body: string(b.draft), saved: true, editIdx: editIdxOf(b)}, nil
		case actDiscard:
			return editResult{editIdx: -1}, nil
		}
	}
}

func editIdxOf(b *browser) int {
	if b.editing {
		return b.editIdx
	}
	return -1
}
