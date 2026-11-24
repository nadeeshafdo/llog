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
	kHome
	kEnd
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
	pos    int
}

// browser holds the state of the writing prompt. The draft being typed is
// always kept; Up/Down only moves the history view, so browsing never loses
// what was typed. pos is the cursor offset in draft, qpos in query.
type browser struct {
	entries   []Entry
	topIdx    int
	draft     []rune
	pos       int
	inPrompt  bool
	query     []rune
	qpos      int
	inSearch  bool
	matches   []int
	midx      int
	nowHeader string
	checker   *spellChecker
	editing   bool
	editIdx   int
	stash     []rune
	stashPos  int
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
		b.qpos = 0
		return actNone
	case kCtrlA:
		b.addWord()
		return actNone
	case kUp, kDown:
		if !b.editing {
			if len(b.entries) > 0 {
				if k.kind == kUp && b.topIdx > 0 {
					b.topIdx--
				}
				if k.kind == kDown && b.topIdx < len(b.entries)-1 {
					b.topIdx++
				}
			}
			return actNone
		}
		b.moveVertical(k.kind == kUp)
		return actNone
	case kLeft:
		if b.pos > 0 {
			b.pos--
		}
		return actNone
	case kRight:
		if b.pos < len(b.draft) {
			b.pos++
		}
		return actNone
	case kHome:
		b.pos = lineStart(b.draft, b.pos)
		return actNone
	case kEnd:
		b.pos = lineEnd(b.draft, b.pos)
		return actNone
	case kEnter:
		b.maybeAutocorrect()
		b.draft = insertAt(b.draft, b.pos, '\n')
		b.pos++
		return actNone
	case kBackspace:
		if b.undo.active {
			b.draft = b.undo.snap
			b.pos = b.undo.pos
			b.undo.active = false
			b.status = "autocorrect undone"
			return actNone
		}
		if b.pos > 0 {
			b.draft = append(b.draft[:b.pos-1], b.draft[b.pos:]...)
			b.pos--
		}
		return actNone
	case kTab:
		if !b.acceptSuggestion() {
			b.draft = insertAt(b.draft, b.pos, '\t')
			b.pos++
		}
		return actNone
	case kRune:
		if isBoundary(k.r) {
			b.maybeAutocorrect()
		}
		b.draft = insertAt(b.draft, b.pos, k.r)
		b.pos++
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
		b.qpos = 0
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
		if b.qpos > 0 && b.qpos <= len(b.query) {
			b.query = append(b.query[:b.qpos-1], b.query[b.qpos:]...)
			b.qpos--
		} else if b.qpos > len(b.query) {
			b.qpos = len(b.query)
		}
		return actNone
	case kLeft:
		if b.qpos > 0 {
			b.qpos--
		}
		return actNone
	case kRight:
		if b.qpos < len(b.query) {
			b.qpos++
		}
		return actNone
	case kHome:
		b.qpos = 0
		return actNone
	case kEnd:
		b.qpos = len(b.query)
		return actNone
	case kRune, kTab:
		r := k.r
		if k.kind == kTab {
			r = '\t'
		}
		b.query = insertAt(b.query, b.qpos, r)
		b.qpos++
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

// insertAt inserts r at rune offset pos.
func insertAt(d []rune, pos int, r ...rune) []rune {
	if pos < 0 {
		pos = 0
	}
	if pos > len(d) {
		pos = len(d)
	}
	out := make([]rune, 0, len(d)+len(r))
	out = append(out, d[:pos]...)
	out = append(out, r...)
	out = append(out, d[pos:]...)
	return out
}

func lineStart(d []rune, pos int) int {
	if pos > len(d) {
		pos = len(d)
	}
	i := pos
	for i > 0 && d[i-1] != '\n' {
		i--
	}
	return i
}

func lineEnd(d []rune, pos int) int {
	if pos < 0 {
		pos = 0
	}
	i := pos
	for i < len(d) && d[i] != '\n' {
		i++
	}
	return i
}

// moveVertical moves the cursor to the previous/next line, keeping the
// column when the line is long enough.
func (b *browser) moveVertical(up bool) {
	start := lineStart(b.draft, b.pos)
	col := b.pos - start
	if up {
		if start == 0 {
			return
		}
		prevEnd := start - 1
		prevStart := lineStart(b.draft, prevEnd)
		b.pos = prevStart + min(col, prevEnd-prevStart)
		return
	}
	end := lineEnd(b.draft, b.pos)
	if end == len(b.draft) {
		return
	}
	nextStart := end + 1
	nextEnd := lineEnd(b.draft, nextStart)
	b.pos = nextStart + min(col, nextEnd-nextStart)
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
	b.stashPos = b.pos
	b.editIdx = b.topIdx
	b.draft = []rune(strings.TrimRight(entryBody(b.entries[b.editIdx]), "\n"))
	b.pos = len(b.draft)
	b.editing = true
	b.status = "editing " + displayHeader(b.entries[b.editIdx].Header)
}

func (b *browser) cancelEdit(msg string) {
	b.draft = b.stash
	b.pos = b.stashPos
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
	if b.pos > len(b.draft) {
		b.pos = len(b.draft)
	}
	word, start := trailingWord(b.draft[:b.pos])
	if word == "" {
		return
	}
	rep, ok := b.checker.fix(word)
	if !ok {
		return
	}
	b.undo.snap = append([]rune(nil), b.draft...)
	b.undo.pos = b.pos
	b.undo.active = true
	b.draft = append(append(append([]rune(nil), b.draft[:start]...), []rune(rep)...), b.draft[b.pos:]...)
	b.pos = start + len([]rune(rep))
	b.status = "autocorrected " + word + " -> " + rep + " (Backspace undoes)"
}

// acceptSuggestion replaces the word before the cursor with the top
// suggestion. It reports whether a replacement happened.
func (b *browser) acceptSuggestion() bool {
	if b.checker == nil || !b.checker.enabled {
		return false
	}
	if b.pos > len(b.draft) {
		b.pos = len(b.draft)
	}
	word, start := trailingWord(b.draft[:b.pos])
	if word == "" || b.checker.check(word) {
		return false
	}
	sugs := b.checker.suggest(word, 1)
	if len(sugs) == 0 {
		b.status = "no suggestion for " + word
		return false
	}
	b.undo.snap = append([]rune(nil), b.draft...)
	b.undo.pos = b.pos
	b.undo.active = true
	b.draft = append(append(append([]rune(nil), b.draft[:start]...), []rune(sugs[0])...), b.draft[b.pos:]...)
	b.pos = start + len([]rune(sugs[0]))
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

// fitLines keeps at most maxRows rows, cutting over-wide rows with an
// ANSI-safe truncator and replacing the tail with a counter when content
// is cut. Rows may carry ANSI highlight codes; the counter never does.
func fitLines(lines []string, maxRows, w int) []string {
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		out = append(out, truncAnsi(ln, w, "…"))
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
	src   int    // plain-rune index the cell starts at (tabs share one index)
}

// emitStyled renders cells, switching SGR codes only on change and always
// resetting an open style, so output never carries redundant or dangling
// codes even when styles were split apart by wrapping.
func emitStyled(cells []styledCell) string {
	var sb strings.Builder
	prev := ""
	for _, c := range cells {
		if c.style != prev {
			if c.style == "" {
				sb.WriteString("\x1b[0m")
			} else {
				sb.WriteString(c.style)
			}
			prev = c.style
		}
		sb.WriteRune(c.r)
	}
	if prev != "" {
		sb.WriteString("\x1b[0m")
	}
	return sb.String()
}

// lineCells splits a line into visible cells, tracking ANSI SGR state.
// Tabs expand to 4 spaces (display only, the model keeps '\t').
// Each cell records the plain-rune index it starts at, so a cursor offset
// can be mapped back onto wrapped rows.
func lineCells(s string) []styledCell {
	var cells []styledCell
	active := ""
	src := 0
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
			for i := 0; i < 4; i++ {
				cells = append(cells, styledCell{' ', active, src})
			}
			src++
			continue
		}
		cells = append(cells, styledCell{r, active, src})
		src++
	}
	return cells
}

func cellWidth(r rune) int {
	if w := runewidth.RuneWidth(r); w > 0 {
		return w
	}
	return 1
}

// truncAnsi cuts s to at most w visible cells without breaking ANSI SGR
// sequences or splitting wide runes. A tail (e.g. "…") is appended when cut.
func truncAnsi(s string, w int, tail string) string {
	if w < 1 {
		return ""
	}
	cells := lineCells(s)
	width := 0
	for _, c := range cells {
		width += cellWidth(c.r)
	}
	if width <= w {
		return s
	}
	tailW := 0
	for _, r := range tail {
		tailW += cellWidth(r)
	}
	limit := w - tailW
	if limit < 0 {
		limit = 0
	}
	var kept []styledCell
	used := 0
	for _, c := range cells {
		cw := cellWidth(c.r)
		if used+cw > limit {
			break
		}
		kept = append(kept, c)
		used += cw
	}
	return emitStyled(kept) + tail
}

// wrapCells lays cells out into rows of at most w visible cells. All
// spacing is preserved exactly so cursor offsets keep mapping 1:1 onto
// rows. Words stay intact unless longer than w; a fresh row never starts
// with wrap filler, only with the line's own leading spaces.
func wrapCells(cells []styledCell, w int) [][]styledCell {
	if w < 1 {
		return [][]styledCell{{}}
	}
	// Split into words (non-space runs) and space runs.
	type chunk struct {
		cells []styledCell
		space bool
	}
	var chunks []chunk
	var cur []styledCell
	curSpace := false
	flushChunk := func() {
		if len(cur) > 0 {
			chunks = append(chunks, chunk{cur, curSpace})
			cur = nil
		}
	}
	for _, c := range cells {
		isSpace := c.r == ' '
		if len(cur) > 0 && isSpace != curSpace {
			flushChunk()
		}
		curSpace = isSpace
		cur = append(cur, c)
	}
	flushChunk()
	if len(chunks) == 0 {
		return [][]styledCell{{}}
	}
	cellW := func(cs []styledCell) int {
		n := 0
		for _, c := range cs {
			n += cellWidth(c.r)
		}
		return n
	}
	var rows [][]styledCell
	var row []styledCell
	rowW := 0
	emit := func() {
		if len(row) > 0 {
			rows = append(rows, row)
			row = nil
			rowW = 0
		}
	}
	for ci, ch := range chunks {
		// A space run starting a fresh row mid-text is wrap filler:
		// drop it instead of indenting the new row. The line's own
		// leading spaces (first chunk) are always kept, split across
		// rows if wider than the screen.
		if ch.space && len(row) == 0 && ci != 0 {
			continue
		}
		ww := cellW(ch.cells)
		if ww > w && (len(row) == 0 || !ch.space) {
			emit()
			for start := 0; start < len(ch.cells); {
				used := 0
				end := start
				for end < len(ch.cells) && used+cellWidth(ch.cells[end].r) <= w {
					used += cellWidth(ch.cells[end].r)
					end++
				}
				if end == start {
					end = start + 1 // rune wider than w: emit anyway
				}
				rows = append(rows, ch.cells[start:end])
				start = end
			}
			continue
		}
		if rowW+ww > w {
			emit()
			if ch.space {
				continue // wrap filler, not the line's own indent
			}
		}
		row = append(row, ch.cells...)
		rowW += ww
	}
	emit()
	if len(rows) == 0 {
		rows = [][]styledCell{{}}
	}
	return rows
}

// wrapLine wraps s to rows of at most w visible cells, word-aware.
func wrapLine(s string, w int) []string {
	rows := wrapCells(lineCells(s), w)
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, emitStyled(r))
	}
	return out
}

// cursorPos maps a plain-rune offset onto wrapped rows, returning the row
// index and the visible column within it. A position between rows sticks
// to the end of the earlier row.
func cursorPos(rows [][]styledCell, p int) (int, int) {
	row, col := 0, 0
	for ri, r := range rows {
		c := 0
		hit := false
		for _, cell := range r {
			if cell.src < p {
				c += cellWidth(cell.r)
				hit = true
			} else {
				break
			}
		}
		if !hit {
			break
		}
		row, col = ri, c
	}
	return row, col
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
	case tea.KeyHome:
		return one(kHome)
	case tea.KeyEnd:
		return one(kEnd)
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

// helpText returns the key hints fitting the current mode.
func (b *browser) helpText() string {
	switch {
	case b.inPrompt:
		return "Type query | Enter browse | Esc cancel | Left/Right/Home/End move"
	case b.inSearch:
		return "Up/Down matches | Esc write | Ctrl+S save | Ctrl+C discard"
	case b.editing:
		return "Type | Arrows/Home/End move | Tab fix | Ctrl+A word | Ctrl+S save | Esc cancel"
	default:
		return "Enter newline | Left/Right/Home/End move | Up/Down history | Ctrl+F search | Ctrl+E edit | Tab fix | Ctrl+A word | Ctrl+S save | Ctrl+C discard"
	}
}

// View lays out title, help, list, divider and input to fill the terminal
// exactly. The input sits last so the real cursor lands in it; a final
// escape parks the cursor at the exact input column.
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
	var bottomCells [][]styledCell
	var bottomCounts []int
	for _, ln := range bottomLines {
		var cellRows [][]styledCell
		for _, r := range wrapCells(lineCells(m.mark(ln)), w) {
			wrappedBottom = append(wrappedBottom, emitStyled(r))
			cellRows = append(cellRows, r)
		}
		if len(cellRows) == 0 {
			cellRows = [][]styledCell{{}}
			wrappedBottom = append(wrappedBottom, "")
		}
		bottomCells = append(bottomCells, cellRows...)
		bottomCounts = append(bottomCounts, len(cellRows))
	}
	bottomRows := len(wrappedBottom)
	if bottomRows < 1 {
		bottomRows = 1
		wrappedBottom = []string{""}
		bottomCells = [][]styledCell{{}}
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
	sb.WriteString(bar(titleStyle, fmt.Sprintf("llog  %s  |  %s", m.b.nowHeader, spellTag(m.b.checker)), w) + "\n")
	sb.WriteString(bar(footStyle, m.b.helpText(), w) + "\n")
	sb.WriteString(headStyle.Render(truncLine(topHead, w)) + "\n")
	// Rows are already marked exactly once above; never mark again here,
	// or the SGR bytes themselves get flagged as words.
	for _, ln := range shown {
		sb.WriteString(ln + "\n")
	}
	sb.WriteString(dimStyle.Render(strings.Repeat("-", w)) + "\n")
	sb.WriteString(headStyle.Render(truncLine(bottomHead, w)) + "\n")
	for _, ln := range wrappedBottom {
		sb.WriteString(ln + "\n")
	}
	s := strings.TrimSuffix(sb.String(), "\n")
	// Park the cursor on the input position.
	crow, ccol := m.cursorRowCol(bottomCounts, bottomCells, bottomLines)
	up := len(wrappedBottom) - crow
	if up < 1 {
		up = 1
	}
	col := ccol + 1
	if col < 1 {
		col = 1
	}
	if col > w {
		col = w
	}
	return s + fmt.Sprintf("\x1b[%dA\x1b[%dG", up, col)
}

// cursorRowCol locates the input cursor (draft/pos or query/qpos) in the
// wrapped bottom rows built by View.
func (m teaModel) cursorRowCol(counts []int, bottomCells [][]styledCell, bottomLines []string) (int, int) {
	content := string(m.b.draft)
	pos := m.b.pos
	if m.b.inPrompt {
		content = string(m.b.query)
		pos = m.b.qpos
	}
	runes := []rune(content)
	if pos < 0 {
		pos = 0
	}
	if pos > len(runes) {
		pos = len(runes)
	}
	off := 0
	base := 0
	for i, ln := range bottomLines {
		n := len([]rune(ln))
		if pos <= off+n || i == len(bottomLines)-1 {
			local := pos - off
			if local < 0 {
				local = 0
			}
			rows := bottomCells[base : base+counts[i]]
			r, c := cursorPos(rows, local)
			return base + r, c
		}
		off += n + 1
		base += counts[i]
	}
	return 0, 0
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
