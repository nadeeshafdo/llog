package main

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	runewidth "github.com/mattn/go-runewidth"
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

var sgrRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

type styledCell struct {
	r     rune
	style string // active SGR code, "" when unstyled
}

// lineCells splits a line into visible cells, tracking ANSI SGR state.
// Tabs expand to 4 spaces (display only, the model keeps '\t').
func lineCells(s string) []styledCell {
	var cells []styledCell
	active := ""
	for len(s) > 0 {
		if loc := sgrRe.FindStringIndex(s); loc != nil && loc[0] == 0 {
			if s[:loc[1]] == "\x1b[0m" {
				active = ""
			} else {
				active = s[:loc[1]]
			}
			s = s[loc[1]:]
			continue
		}
		r, size := utf8.DecodeRuneInString(s)
		s = s[size:]
		if r == '\t' {
			// Non-breaking spaces: survive space-collapsing so tab
			// stops keep their width on display.
			for i := 0; i < 4; i++ {
				cells = append(cells, styledCell{'\u00a0', active})
			}
			continue
		}
		cells = append(cells, styledCell{r, active})
	}
	return cells
}

func cellWidth(r rune) int {
	if w := runewidth.RuneWidth(r); w > 0 {
		return w
	}
	return 1
}

// wrapLine wraps s to rows of at most w visible cells. Wrapping is
// word-aware; words longer than w are hard-split. ANSI styles reopen after
// every break so rows stay self-contained. Runs of spaces collapse to one
// (display only).
func wrapLine(s string, w int) []string {
	if w < 1 {
		return []string{""}
	}
	cells := lineCells(s)
	var words [][]styledCell
	var cur []styledCell
	for _, c := range cells {
		if c.r == ' ' {
			if len(cur) > 0 {
				words = append(words, cur)
				cur = nil
			}
			continue
		}
		cur = append(cur, c)
	}
	if len(cur) > 0 {
		words = append(words, cur)
	}
	if len(words) == 0 {
		return []string{""}
	}
	flush := func(row []styledCell) string {
		var sb strings.Builder
		prev := ""
		for _, c := range row {
			if c.style != prev {
				sb.WriteString(c.style)
				prev = c.style
			}
			sb.WriteRune(c.r)
		}
		if prev != "" {
			sb.WriteString("\x1b[0m")
		}
		return sb.String()
	}
	var rows []string
	var row []styledCell
	rowW := 0
	for _, wd := range words {
		ww := 0
		for _, c := range wd {
			ww += cellWidth(c.r)
		}
		if ww > w {
			if len(row) > 0 {
				rows = append(rows, flush(row))
				row = nil
				rowW = 0
			}
			for start := 0; start < len(wd); {
				used := 0
				end := start
				for end < len(wd) && used+cellWidth(wd[end].r) <= w {
					used += cellWidth(wd[end].r)
					end++
				}
				if end == start {
					end = start + 1 // rune wider than w: emit anyway
				}
				rows = append(rows, flush(wd[start:end]))
				start = end
			}
			continue
		}
		need := ww
		if len(row) > 0 {
			need++ // separating space
		}
		if rowW+need > w {
			rows = append(rows, flush(row))
			row = nil
			rowW = 0
		}
		if len(row) > 0 {
			row = append(row, styledCell{' ', ""})
			rowW++
		}
		row = append(row, wd...)
		rowW += ww
	}
	if len(row) > 0 {
		rows = append(rows, flush(row))
	}
	return rows
}

// keyFromMsg converts a Bubble Tea key message into one or more internal
// keys. Pasted text arrives as runes and is fed through one rune at a time.
func keyFromMsg(msg tea.KeyMsg) []key {
	one := func(k keyKind) []key { return []key{{kind: k}} }
	oneRune := func(r rune) []key { return []key{{kind: kRune, r: r}} }
	switch msg.Type {
	case tea.KeyEnter:
		return one(kEnter)
	case tea.KeyBackspace, tea.KeyDelete:
		return one(kBackspace)
	case tea.KeyTab:
		return one(kTab)
	case tea.KeyUp:
		return one(kUp)
	case tea.KeyDown:
		return one(kDown)
	case tea.KeyLeft:
		return one(kLeft)
	case tea.KeyRight:
		return one(kRight)
	case tea.KeyEsc:
		return one(kEsc)
	case tea.KeySpace:
		return oneRune(' ')
	case tea.KeyRunes:
		out := make([]key, 0, len(msg.Runes))
		for _, r := range msg.Runes {
			switch {
			case r == '\r' || r == '\n':
				out = append(out, key{kind: kEnter})
			case r == '\t':
				out = append(out, key{kind: kTab})
			case r == 0x20 || (r > 0x20 && r != 0x7f):
				out = append(out, key{kind: kRune, r: r})
			default:
				out = append(out, key{kind: kUnknown})
			}
		}
		return out
	}
	switch msg.String() {
	case "ctrl+c":
		return one(kCtrlC)
	case "ctrl+s", "ctrl+d":
		return one(kCtrlS)
	case "ctrl+f":
		return one(kCtrlF)
	case "ctrl+e":
		return one(kCtrlE)
	case "ctrl+a":
		return one(kCtrlA)
	default:
		return one(kUnknown)
	}
}

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(lipgloss.Color("#44475A"))
	headStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#8BE9FD"))
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#6272A4"))
	footStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#F8F8F2")).Background(lipgloss.Color("#44475A"))
	spellTagOn  = "spell on"
	spellTagOff = "spell off"
)

// bar renders a full-width bar without wrapping: truncate, pad, style.
func bar(style lipgloss.Style, text string, w int) string {
	t := truncLine(text, w)
	if pad := w - lipgloss.Width(t); pad > 0 {
		t += strings.Repeat(" ", pad)
	}
	return style.Render(t)
}

func spellTag(checker *spellChecker) string {
	if checker != nil && checker.enabled {
		return spellTagOn
	}
	return spellTagOff
}

// teaModel is the Bubble Tea program. All journal state lives in browser;
// this layer only maps messages to it and lays out the view.
type teaModel struct {
	b      *browser
	width  int
	height int
	result editResult
}

func (m teaModel) Init() tea.Cmd { return nil }

func (m teaModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.KeyMsg:
		for _, k := range keyFromMsg(msg) {
			switch m.b.handle(k) {
			case actSave:
				idx := -1
				if m.b.editing {
					idx = m.b.editIdx
				}
				m.result = editResult{body: string(m.b.draft), saved: true, editIdx: idx}
				return m, tea.Quit
			case actDiscard:
				m.result = editResult{editIdx: -1}
				return m, tea.Quit
			}
		}
		return m, nil
	}
	return m, nil
}

// View lays out title, list, divider, input and help to fill the terminal
// exactly. The cursor is parked at the end of the input afterwards.
func (m teaModel) View() string {
	w, h := m.width, m.height
	if w < 20 {
		w = 20
	}
	if h < 10 {
		h = 10
	}
	topHead, topBody := m.b.topPane()
	bottomHead, bottomLines := m.b.bottomPane()
	var wrappedBottom []string
	for _, ln := range bottomLines {
		wrappedBottom = append(wrappedBottom, wrapLine(m.mark(ln), w)...)
	}
	bottomRows := len(wrappedBottom)
	if bottomRows < 1 {
		bottomRows = 1
		wrappedBottom = []string{""}
	}
	maxBody := h - 5 - bottomRows
	if maxBody < 1 {
		maxBody = 1
	}
	var rows []string
	for _, ln := range topBody {
		rows = append(rows, wrapLine(m.mark(ln), w)...)
	}
	shown := fitLines(rows, maxBody, w)
	for len(shown) < maxBody {
		shown = append(shown, "")
	}
	var sb strings.Builder
	sb.WriteString(bar(titleStyle, fmt.Sprintf("llog  %s  |  Ctrl+S save  Ctrl+C discard  Up/Down history  Ctrl+F search  Ctrl+E edit  Tab fix  |  %s", m.b.nowHeader, spellTag(m.b.checker)), w) + "\n")
	sb.WriteString(headStyle.Render(truncLine(topHead, w)) + "\n")
	for _, ln := range shown {
		sb.WriteString(m.mark(ln) + "\n")
	}
	sb.WriteString(dimStyle.Render(strings.Repeat("-", w)) + "\n")
	sb.WriteString(headStyle.Render(truncLine(bottomHead, w)) + "\n")
	for _, ln := range wrappedBottom {
		sb.WriteString(ln + "\n")
	}
	sb.WriteString(bar(footStyle, "Enter newline | Backspace delete | Up/Down history | Ctrl+F search | Ctrl+E edit | Tab fix | Ctrl+A word | Ctrl+S save | Ctrl+C discard", w))
	s := strings.TrimSuffix(sb.String(), "\n")
	col := lipgloss.Width(wrappedBottom[len(wrappedBottom)-1]) + 1
	if col < 1 {
		col = 1
	}
	if col > w {
		col = w
	}
	return s + fmt.Sprintf("\x1b[1A\x1b[%dG", col)
}

func (m teaModel) mark(line string) string {
	if m.b.checker == nil {
		return line
	}
	return m.b.checker.mark(line)
}

// tuiLoop runs the full-screen prompt and returns the outcome.
// Terminal setup/teardown (raw mode, alt screen) is owned by Bubble Tea.
// Errors and EOF (pty closed) return unsaved: fail closed, never append
// unverified bytes.
func tuiLoop(inFile, outFile *os.File, entries []Entry, nowHeader string, checker *spellChecker) (editResult, error) {
	m := teaModel{b: newBrowser(entries, nowHeader, checker), width: 80, height: 24}
	p := tea.NewProgram(m, tea.WithInput(inFile), tea.WithOutput(outFile), tea.WithAltScreen())
	final, err := p.Run()
	if err != nil {
		return editResult{editIdx: -1}, err
	}
	tm, ok := final.(teaModel)
	if !ok {
		return editResult{editIdx: -1}, errors.New("unexpected model type")
	}
	return tm.result, nil
}
