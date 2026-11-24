package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func testEntries() []Entry {
	return []Entry{
		{Header: "2002..", Text: "2002.. first memory about apple"},
		{Header: "2026/05/03..", Text: "2026/05/03.. went to the church"},
		{Header: "2026/11/19 18:30..", Text: "2026/11/19 18:30.. banana day"},
	}
}

func press(b *browser, k key) action {
	return b.handle(k)
}

func runeKey(r rune) key {
	return key{kind: kRune, r: r}
}

func typeText(b *browser, s string) {
	for _, r := range s {
		press(b, runeKey(r))
	}
}

func testModel(checker *spellChecker) teaModel {
	return teaModel{b: newBrowser(testEntries(), "2026/11/19 18:30..", checker), width: 60, height: 20}
}

func sendKeys(m teaModel, msgs ...tea.KeyMsg) teaModel {
	for _, msg := range msgs {
		updated, _ := m.Update(msg)
		m = updated.(teaModel)
	}
	return m
}

func runeMsgs(s string) []tea.KeyMsg {
	var out []tea.KeyMsg
	for _, r := range s {
		out = append(out, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return out
}

func TestBrowserHistoryKeepsDraft(t *testing.T) {
	b := newBrowser(testEntries(), "2026/11/19 18:30..", nil)
	if b.topIdx != 2 {
		t.Fatalf("should start at newest, got %d", b.topIdx)
	}
	typeText(b, "my draft")
	if press(b, key{kind: kUp}) != actNone || b.topIdx != 1 {
		t.Fatalf("up should go older, idx=%d", b.topIdx)
	}
	if press(b, key{kind: kUp}) != actNone || b.topIdx != 0 {
		t.Fatalf("up should go older, idx=%d", b.topIdx)
	}
	if press(b, key{kind: kUp}) != actNone || b.topIdx != 0 {
		t.Fatalf("up should clamp at oldest, idx=%d", b.topIdx)
	}
	if string(b.draft) != "my draft" {
		t.Fatalf("draft lost while browsing: %q", string(b.draft))
	}
	if press(b, key{kind: kDown}) != actNone || b.topIdx != 1 {
		t.Fatalf("down should go newer, idx=%d", b.topIdx)
	}
	if press(b, key{kind: kDown}) != actNone || b.topIdx != 2 {
		t.Fatalf("down should go newer, idx=%d", b.topIdx)
	}
	if press(b, key{kind: kDown}) != actNone || b.topIdx != 2 {
		t.Fatalf("down should clamp at newest, idx=%d", b.topIdx)
	}
}

func TestBrowserSaveDiscard(t *testing.T) {
	b := newBrowser(testEntries(), "h", nil)
	typeText(b, "x")
	if press(b, key{kind: kCtrlS}) != actSave {
		t.Fatal("ctrl-s should save")
	}
	b2 := newBrowser(testEntries(), "h", nil)
	typeText(b2, "x")
	if press(b2, key{kind: kCtrlC}) != actDiscard {
		t.Fatal("ctrl-c should discard")
	}
}

func TestBrowserSearchFlow(t *testing.T) {
	b := newBrowser(testEntries(), "h", nil)
	typeText(b, "draft here")
	if press(b, key{kind: kCtrlF}) != actNone || !b.inPrompt {
		t.Fatal("ctrl-f should open search prompt")
	}
	typeText(b, "church")
	if string(b.query) != "church" {
		t.Fatalf("query = %q", string(b.query))
	}
	if press(b, key{kind: kEnter}) != actNone || !b.inSearch {
		t.Fatal("enter should enter search view")
	}
	if len(b.matches) != 1 || b.matches[0] != 1 {
		t.Fatalf("matches = %v", b.matches)
	}
	if press(b, key{kind: kEsc}) != actNone || b.inSearch {
		t.Fatal("esc should leave search view")
	}
	if string(b.draft) != "draft here" {
		t.Fatalf("draft lost across search: %q", string(b.draft))
	}
}

func TestBrowserSearchWrapsAndLocksDraft(t *testing.T) {
	entries := []Entry{
		{Header: "2001..", Text: "2001.. apple pie"},
		{Header: "2002..", Text: "2002.. banana"},
		{Header: "2003..", Text: "2003.. APPLE again"},
	}
	b := newBrowser(entries, "h", nil)
	press(b, key{kind: kCtrlF})
	typeText(b, "apple")
	press(b, key{kind: kEnter})
	if len(b.matches) != 2 {
		t.Fatalf("matches = %v", b.matches)
	}
	start := b.midx
	press(b, key{kind: kUp})
	press(b, key{kind: kDown})
	if b.midx != start {
		t.Fatalf("up then down should return, midx=%d", b.midx)
	}
	before := string(b.draft)
	typeText(b, "zzz")
	if string(b.draft) != before {
		t.Fatal("typing in search view must not touch the draft")
	}
}

func TestBrowserSearchNoMatchStaysInPrompt(t *testing.T) {
	b := newBrowser(testEntries(), "h", nil)
	press(b, key{kind: kCtrlF})
	typeText(b, "zzz-nope")
	if press(b, key{kind: kEnter}) != actNone || !b.inPrompt || b.inSearch {
		t.Fatal("no matches should stay in the prompt")
	}
}

func TestFitLines(t *testing.T) {
	lines := []string{"one", "two", "three", "four"}
	got := fitLines(lines, 3, 80)
	if len(got) != 3 || !strings.HasPrefix(got[2], "... (") {
		t.Fatalf("got %v", got)
	}
	got = fitLines([]string{"averylongline"}, 5, 4)
	if got[0] != "ave…" {
		t.Fatalf("width truncation failed: %q", got[0])
	}
}

func TestViewPanes(t *testing.T) {
	m := testModel(nil)
	out := m.View()
	for _, want := range []string{"[history 3/3]", "banana day", "-- new entry --"} {
		if !strings.Contains(out, want) {
			t.Fatalf("view missing %q in:\n%s", want, out)
		}
	}
	b := newBrowser(testEntries(), "2026/11/19 18:30..", nil)
	press(b, key{kind: kCtrlF})
	typeText(b, "church")
	m2 := teaModel{b: b, width: 60, height: 20}
	if out := m2.View(); !strings.Contains(out, "-- search: church (1 matches") {
		t.Fatalf("search view wrong:\n%s", out)
	}
}

func TestViewFillsHeight(t *testing.T) {
	for _, h := range []int{15, 20, 30} {
		m := teaModel{b: newBrowser(testEntries(), "2026/11/19 18:30..", nil), width: 60, height: h}
		out := m.View()
		if n := strings.Count(out, "\n"); n != h-1 {
			t.Fatalf("height %d: view has %d newlines, want %d", h, n, h-1)
		}
	}
}

func TestViewHighlightsTypo(t *testing.T) {
	sc := testChecker()
	sc.customPath = ""
	b := newBrowser(testEntries(), "h", sc)
	typeText(b, "hello teh")
	m := teaModel{b: b, width: 60, height: 20}
	if out := m.View(); !strings.Contains(out, "\x1b[31mteh\x1b[0m") {
		t.Fatalf("view should highlight typo:\n%s", out)
	}
}

func TestKeyFromMsg(t *testing.T) {
	cases := []struct {
		msg  tea.KeyMsg
		want keyKind
		r    rune
	}{
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}}, kRune, 'a'},
		{tea.KeyMsg{Type: tea.KeySpace}, kRune, ' '},
		{tea.KeyMsg{Type: tea.KeyEnter}, kEnter, 0},
		{tea.KeyMsg{Type: tea.KeyBackspace}, kBackspace, 0},
		{tea.KeyMsg{Type: tea.KeyTab}, kTab, 0},
		{tea.KeyMsg{Type: tea.KeyUp}, kUp, 0},
		{tea.KeyMsg{Type: tea.KeyDown}, kDown, 0},
		{tea.KeyMsg{Type: tea.KeyLeft}, kLeft, 0},
		{tea.KeyMsg{Type: tea.KeyRight}, kRight, 0},
		{tea.KeyMsg{Type: tea.KeyEsc}, kEsc, 0},
		{tea.KeyMsg{Type: tea.KeyCtrlC}, kCtrlC, 0},
	}
	for _, c := range cases {
		got := keyFromMsg(c.msg)
		if len(got) != 1 || got[0].kind != c.want || got[0].r != c.r {
			t.Fatalf("msg %v gave %v", c.msg, got)
		}
	}
}

func TestKeyFromMsgCtrl(t *testing.T) {
	cases := []struct {
		msg  tea.KeyMsg
		want keyKind
	}{
		{tea.KeyMsg{Type: tea.KeyCtrlS}, kCtrlS},
		{tea.KeyMsg{Type: tea.KeyCtrlD}, kCtrlS},
		{tea.KeyMsg{Type: tea.KeyCtrlF}, kCtrlF},
		{tea.KeyMsg{Type: tea.KeyCtrlE}, kCtrlE},
		{tea.KeyMsg{Type: tea.KeyCtrlA}, kCtrlA},
		{tea.KeyMsg{Type: tea.KeyCtrlC}, kCtrlC},
	}
	for _, c := range cases {
		got := keyFromMsg(c.msg)
		if len(got) != 1 || got[0].kind != c.want {
			t.Fatalf("msg %v gave %v, want kind %v", c.msg, got, c.want)
		}
	}
	paste := keyFromMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h', 'i'}})
	if len(paste) != 2 || paste[0].r != 'h' || paste[1].r != 'i' {
		t.Fatalf("paste gave %v", paste)
	}
	enter := keyFromMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'\n'}})
	if len(enter) != 1 || enter[0].kind != kEnter {
		t.Fatalf("pasted newline gave %v", enter)
	}
}

func TestUpdateEscCancelsEdit(t *testing.T) {
	m := testModel(nil)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m = updated.(teaModel)
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	m = updated.(teaModel)
	if cmd != nil || !m.b.editing {
		t.Fatal("ctrl-e should start editing without quitting")
	}
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(teaModel)
	if cmd != nil {
		t.Fatal("esc cancel should not quit")
	}
	if m.b.editing {
		t.Fatal("esc should cancel editing")
	}
	if string(m.b.draft) != "x" {
		t.Fatalf("stash should be restored, got %q", string(m.b.draft))
	}
}

func TestUpdateSaveQuits(t *testing.T) {
	m := testModel(nil)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = updated.(teaModel)
	if cmd == nil {
		t.Fatal("ctrl-s should quit")
	}
	if !m.result.saved || m.result.editIdx != -1 {
		t.Fatalf("result = %+v", m.result)
	}
}

func editTestChecker() *spellChecker {
	sc := testChecker()
	sc.customPath = ""
	return sc
}

func TestBrowserStartEditAndCancel(t *testing.T) {
	b := newBrowser(testEntries(), "h", nil)
	typeText(b, "new draft")
	b.topIdx = 1
	if press(b, key{kind: kCtrlE}) != actNone || !b.editing {
		t.Fatal("ctrl-e should start editing")
	}
	if b.editIdx != 1 {
		t.Fatalf("editIdx = %d", b.editIdx)
	}
	if string(b.draft) != "went to the church" {
		t.Fatalf("draft should hold entry body, got %q", string(b.draft))
	}
	if press(b, key{kind: kEsc}) != actNone || b.editing {
		t.Fatal("esc should cancel editing")
	}
	if string(b.draft) != "new draft" {
		t.Fatalf("stash should be restored, got %q", string(b.draft))
	}
}

func TestBrowserEditLocksNavigation(t *testing.T) {
	b := newBrowser(testEntries(), "h", nil)
	press(b, key{kind: kCtrlE})
	idx := b.topIdx
	press(b, key{kind: kUp})
	if b.topIdx != idx {
		t.Fatal("navigation should be locked while editing")
	}
	if b.status == "" {
		t.Fatal("locked navigation should explain itself")
	}
}

func TestBrowserAutocorrectAndUndo(t *testing.T) {
	b := newBrowser(testEntries(), "h", editTestChecker())
	for _, r := range "teh " {
		press(b, key{kind: kRune, r: r})
	}
	if string(b.draft) != "the " {
		t.Fatalf("autocorrect failed, got %q", string(b.draft))
	}
	if press(b, key{kind: kBackspace}) != actNone {
		t.Fatal("undo backspace failed")
	}
	if string(b.draft) != "teh" {
		t.Fatalf("undo failed, got %q", string(b.draft))
	}
}

func TestBrowserAutocorrectLeavesNamesAlone(t *testing.T) {
	b := newBrowser(testEntries(), "h", editTestChecker())
	typeText(b, "Pesandu ")
	if string(b.draft) != "Pesandu " {
		t.Fatalf("name should be untouched, got %q", string(b.draft))
	}
}

func TestBrowserTabAccept(t *testing.T) {
	b := newBrowser(testEntries(), "h", editTestChecker())
	typeText(b, "chruch")
	if press(b, key{kind: kTab}) != actNone {
		t.Fatal("tab failed")
	}
	if string(b.draft) != "church" {
		t.Fatalf("got %q", string(b.draft))
	}
	press(b, key{kind: kBackspace})
	if string(b.draft) != "chruch" {
		t.Fatalf("tab undo failed, got %q", string(b.draft))
	}
}

func TestBrowserAddWord(t *testing.T) {
	dir := t.TempDir()
	sc := testChecker()
	sc.customPath = dir + "/words.txt"
	b := newBrowser(testEntries(), "h", sc)
	typeText(b, "zqxjk")
	press(b, key{kind: kCtrlA})
	if !sc.check("zqxjk") {
		t.Fatal("added word should be accepted")
	}
	if b.status == "" {
		t.Fatal("add should confirm")
	}
}
