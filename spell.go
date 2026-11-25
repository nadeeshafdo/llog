package main

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// coreWords is a small fallback vocabulary used when no system dictionary is
// available. The real dictionary comes from the system hunspell files.
var coreWords = []string{
	"a", "about", "above", "after", "again", "against", "ago", "ahead", "air", "all",
	"almost", "alone", "along", "already", "also", "always", "am", "an", "and", "angry",
	"another", "any", "anyone", "anything", "are", "around", "as", "ask", "at", "away",
	"back", "bad", "be", "because", "became", "bed", "before", "behind", "being", "best",
	"better", "between", "big", "birthday", "both", "brought", "busy", "but", "by", "call",
	"came", "can", "cannot", "car", "care", "celebrated", "certain", "change", "church", "clean",
	"close", "college", "come", "company", "could", "course", "crazy", "dad", "day", "days",
	"dead", "decided", "did", "didn", "different", "dinner", "do", "does", "doing", "don",
	"done", "door", "down", "dream", "drink", "during", "each", "early", "easy", "eat",
	"electric", "emotional", "end", "enough", "even", "evening", "event", "ever", "every", "everyone",
	"everything", "exam", "eye", "family", "far", "father", "feel", "feeling", "fell", "felt",
	"few", "finally", "find", "fine", "first", "fix", "follow", "food", "for", "forget",
	"found", "friend", "friends", "from", "front", "fun", "funny", "further", "gave", "get",
	"girl", "give", "go", "god", "going", "good", "got", "grandma", "great", "guilt",
	"guilty", "had", "half", "hand", "happen", "happened", "happy", "hard", "has", "have",
	"he", "head", "hear", "heard", "heart", "hello", "help", "her", "here", "herself",
	"hey", "hi", "him", "himself", "his", "home", "hope", "hospital", "hour", "house",
	"how", "however", "human", "hungry", "i", "idea", "if", "ill", "in", "inside",
	"instead", "internship", "into", "is", "it", "its", "job", "just", "keep", "kind",
	"knew", "know", "lab", "laptop", "last", "late", "later", "laugh", "learn", "learned",
	"least", "left", "let", "life", "light", "like", "line", "little", "live", "lived",
	"long", "look", "looked", "lorry", "lost", "lot", "love", "lunch", "made", "make",
	"man", "many", "may", "maybe", "me", "mean", "meant", "memory", "met", "might",
	"mind", "mine", "minute", "missing", "mom", "moment", "money", "month", "months", "morning",
	"mother", "movie", "much", "mum", "my", "myself", "near", "need", "needed", "never",
	"new", "night", "no", "noise", "nor", "normal", "not", "note", "nothing", "now",
	"number", "nurse", "of", "off", "often", "old", "on", "once", "one", "only",
	"open", "or", "other", "our", "out", "outside", "over", "own", "paid", "parents",
	"part", "party", "pass", "passed", "people", "phone", "place", "plan", "play", "played",
	"please", "power", "pray", "pretty", "project", "put", "quite", "rain", "read", "reading",
	"really", "reason", "remember", "repeat", "report", "rest", "result", "results", "room", "round",
	"run", "sad", "said", "same", "saw", "say", "scared", "school", "see", "seen",
	"self", "sent", "she", "should", "show", "sick", "side", "since", "sister", "sit",
	"sleep", "small", "so", "software", "sold", "some", "someone", "something", "sometimes", "soon",
	"sorry", "speak", "spend", "spent", "started", "stay", "still", "storm", "story", "street",
	"student", "study", "studying", "such", "sudden", "summer", "sunday", "sure", "table", "take",
	"taken", "talk", "talked", "tank", "team", "tell", "than", "thank", "that", "the",
	"their", "them", "then", "there", "these", "they", "thing", "things", "think", "third",
	"this", "those", "though", "thought", "three", "through", "time", "tired", "to", "today",
	"together", "told", "tomorrow", "too", "took", "toward", "town", "training", "tried", "truck",
	"truly", "try", "trying", "turn", "two", "under", "understand", "university", "unless", "until",
	"up", "upon", "us", "used", "usually", "vehicle", "very", "visit", "viva", "voice",
	"wait", "wake", "walk", "want", "wanted", "was", "watch", "watched", "water", "way",
	"we", "week", "well", "went", "were", "what", "when", "where", "whether", "which",
	"while", "white", "who", "whole", "whose", "why", "will", "win", "wind", "window",
	"with", "without", "woke", "woman", "wonder", "word", "work", "worked", "working", "works",
	"world", "worry", "would", "write", "writing", "written", "wrong", "wrote", "year", "years",
	"yes", "yesterday", "yet", "you", "young", "your",
}

// typoFixes maps common misspellings (none of which are valid words) to
// their corrections. Applied only to all-lowercase words.
var typoFixes = map[string]string{
	"adn": "and", "aobut": "about", "arguement": "argument", "becuase": "because",
	"begining": "beginning", "beleive": "believe", "calender": "calendar",
	"casue": "cause", "changeing": "changing", "collegue": "colleague",
	"comeing": "coming", "comming": "coming", "concious": "conscious",
	"coudl": "could", "danceing": "dancing", "definately": "definitely",
	"dissapear": "disappear", "embarass": "embarrass", "existance": "existence",
	"experiance": "experience", "freind": "friend", "geting": "getting",
	"giveing": "giving", "goverment": "government", "grammer": "grammar",
	"happend": "happened", "happyness": "happiness", "haveing": "having",
	"hte": "the", "immediatly": "immediately", "jsut": "just",
	"knowlege": "knowledge", "liason": "liaison", "libary": "library",
	"liveing": "living", "maintanance": "maintenance", "makeing": "making", "manageing": "managing", "millenium": "millennium",
	"mispell": "misspell", "nad": "and", "neccessary": "necessary",
	"noticable": "noticeable", "occured": "occurred", "paralel": "parallel",
	"pasttime": "pastime", "persue": "pursue", "posession": "possession",
	"prefered": "preferred", "publically": "publicly", "recomend": "recommend",
	"recieve": "receive", "refered": "referred", "rember": "remember",
	"remeber": "remember", "runing": "running", "rythm": "rhythm",
	"seperate": "separate", "shoudl": "should", "siad": "said",
	"studing": "studying", "succede": "succeed", "suprise": "surprise",
	"taht": "that", "takeing": "taking", "teh": "the", "tendancy": "tendency",
	"thier": "their", "thsi": "this", "tomorow": "tomorrow", "truely": "truly",
	"untill": "until", "useing": "using", "vacum": "vacuum",
	"whcih": "which", "wierd": "weird", "woudl": "would", "writen": "written",
	"writting": "writing",
}

type spellChecker struct {
	words       map[string]bool
	byLen       map[int][]string
	customPath  string
	enabled     bool
	autoCorrect bool
	color       bool
	source      string
}

func defaultCustomPath() string {
	return filepath.Join(configDir(), "words.txt")
}

func configDir() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "llog")
}

// loadChecker builds a spell checker from the system dictionary, the words
// already in the log (names and project terms), and the user's custom words.
func loadChecker(entries []Entry) *spellChecker {
	sc := &spellChecker{
		words:       make(map[string]bool),
		autoCorrect: os.Getenv("LLOG_AUTOCORRECT") != "0",
		color:       os.Getenv("NO_COLOR") == "" && os.Getenv("LLOG_NO_COLOR") == "",
		customPath:  defaultCustomPath(),
	}
	for _, w := range coreWords {
		sc.words[w] = true
	}
	systemLoaded := sc.loadSystemDict()
	threshold := 2
	if !systemLoaded {
		threshold = 1
	}
	sc.addLogWords(entries, threshold)
	sc.loadCustomWords()
	sc.rebuildIndex()
	if systemLoaded {
		sc.source = "hunspell"
	} else {
		sc.source = "built-in"
	}
	sc.enabled = systemLoaded || len(sc.words) >= 500
	return sc
}

func systemDictPaths() []string {
	var paths []string
	if p := os.Getenv("LLOG_DICT"); p != "" {
		paths = append(paths, p)
	}
	paths = append(paths,
		"/usr/share/hunspell/en_US.dic",
		"/usr/share/hunspell/en_GB.dic",
		"/usr/share/dict/words",
	)
	return paths
}

func (sc *spellChecker) loadSystemDict() bool {
	for _, p := range systemDictPaths() {
		n := sc.loadWordFile(p, true)
		if n > 1000 {
			return true
		}
	}
	return false
}

// loadWordFile loads one word per line, stripping hunspell affix flags.
// When hunspell is true the numeric first line (word count) is skipped.
func (sc *spellChecker) loadWordFile(path string, hunspell bool) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	added := 0
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if i == 0 && hunspell {
			if _, err := strconv.Atoi(line); err == nil {
				continue
			}
		}
		if idx := strings.IndexAny(line, "/ \t"); idx >= 0 {
			line = line[:idx]
		}
		w := normalizeWord(line)
		if w == "" {
			continue
		}
		if !sc.words[w] {
			sc.words[w] = true
			added++
		}
	}
	return added
}

func normalizeWord(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.Trim(s, "'")
	if s == "" {
		return ""
	}
	for _, r := range s {
		if !unicode.IsLetter(r) && r != '\'' {
			return ""
		}
	}
	return s
}

func (sc *spellChecker) addLogWords(entries []Entry, threshold int) {
	freq := make(map[string]int)
	for _, e := range entries {
		for _, tok := range splitWords(e.Text) {
			if hasDigit(tok) || (isAllCaps(tok) && len(tok) > 1) {
				continue
			}
			w := normalizeWord(tok)
			if len([]rune(w)) < 2 {
				continue
			}
			freq[w]++
		}
	}
	for w, n := range freq {
		if n >= threshold {
			sc.words[w] = true
		}
	}
}

func (sc *spellChecker) loadCustomWords() {
	if sc.customPath == "" {
		return
	}
	data, err := os.ReadFile(sc.customPath)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		if w := normalizeWord(line); w != "" {
			sc.words[w] = true
		}
	}
}

// addCustom adds a word for this session and persists it.
func (sc *spellChecker) addCustom(word string) error {
	w := normalizeWord(word)
	if w == "" {
		return nil
	}
	sc.words[w] = true
	l := len([]rune(w))
	sc.byLen[l] = append(sc.byLen[l], w)
	if sc.customPath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(sc.customPath), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(sc.customPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(w + "\n")
	return err
}

func (sc *spellChecker) rebuildIndex() {
	sc.byLen = make(map[int][]string)
	for w := range sc.words {
		l := len([]rune(w))
		sc.byLen[l] = append(sc.byLen[l], w)
	}
}

// check reports whether word is spelled correctly.
func (sc *spellChecker) check(word string) bool {
	if word == "" || hasDigit(word) {
		return true
	}
	if isAllCaps(word) && len([]rune(word)) > 1 {
		return true
	}
	lower := strings.ToLower(word)
	if sc.words[lower] {
		return true
	}
	plain := strings.ReplaceAll(lower, "'", "")
	if plain != lower && sc.words[plain] {
		return true
	}
	// Possessives and contractions built on known stems.
	if stem, ok := strings.CutSuffix(lower, "'s"); ok && sc.words[stem] {
		return true
	}
	if lower == "can't" && sc.words["can"] {
		return true
	}
	if lower == "won't" && sc.words["will"] {
		return true
	}
	if stem, ok := strings.CutSuffix(lower, "n't"); ok && sc.words[stem] {
		return true
	}
	if strings.Contains(lower, "'") {
		ok := true
		for _, part := range strings.Split(lower, "'") {
			if part == "" {
				continue
			}
			if !sc.words[part] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// suggest returns up to n corrections ordered by distance, then alphabetically.
func (sc *spellChecker) suggest(word string, n int) []string {
	clean := strings.ReplaceAll(strings.ToLower(word), "'", "")
	if clean == "" {
		return nil
	}
	type cand struct {
		w string
		d int
	}
	var cands []cand
	cr := []rune(clean)
	for dl := -2; dl <= 2; dl++ {
		for _, w := range sc.byLen[len(cr)+dl] {
			if w == clean {
				continue
			}
			if d := levenshteinCutoff(cr, []rune(w), 2); d <= 2 {
				cands = append(cands, cand{w, d})
			}
		}
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].d != cands[j].d {
			return cands[i].d < cands[j].d
		}
		return cands[i].w < cands[j].w
	})
	var out []string
	for i := 0; i < len(cands) && i < n; i++ {
		out = append(out, cands[i].w)
	}
	return out
}

// fix returns an autocorrection for an all-lowercase word.
func (sc *spellChecker) fix(word string) (string, bool) {
	if !sc.autoCorrect || word == "" {
		return "", false
	}
	if word != strings.ToLower(word) {
		return "", false
	}
	if sc.check(word) {
		return "", false
	}
	if rep, ok := typoFixes[word]; ok {
		return rep, true
	}
	if len([]rune(word)) < 4 {
		return "", false
	}
	cr := []rune(word)
	var ones []string
	for dl := -1; dl <= 1; dl++ {
		for _, w := range sc.byLen[len(cr)+dl] {
			if levenshteinCutoff(cr, []rune(w), 1) == 1 {
				ones = append(ones, w)
				if len(ones) > 1 {
					return "", false
				}
			}
		}
	}
	if len(ones) == 1 {
		return ones[0], true
	}
	return "", false
}

// mark wraps misspelled words in red. It assumes no ANSI codes are present.
func (sc *spellChecker) mark(line string) string {
	if sc == nil || !sc.enabled || !sc.color {
		return line
	}
	var sb strings.Builder
	for _, tok := range splitTokens(line) {
		if isWordToken(tok) && !sc.check(tok) {
			sb.WriteString("\x1b[31m" + tok + "\x1b[0m")
			continue
		}
		sb.WriteString(tok)
	}
	return sb.String()
}

func splitTokens(s string) []string {
	var out []string
	var cur strings.Builder
	isWord := false
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range s {
		w := unicode.IsLetter(r) || r == '\''
		if cur.Len() == 0 {
			isWord = w
			cur.WriteRune(r)
			continue
		}
		if w == isWord {
			cur.WriteRune(r)
			continue
		}
		flush()
		isWord = w
		cur.WriteRune(r)
	}
	flush()
	return out
}

func isWordToken(tok string) bool {
	for _, r := range tok {
		if unicode.IsLetter(r) || r == '\'' {
			return true
		}
	}
	return false
}

func splitWords(s string) []string {
	var out []string
	for _, tok := range splitTokens(s) {
		if isWordToken(tok) {
			out = append(out, tok)
		}
	}
	return out
}

func hasDigit(s string) bool {
	for _, r := range s {
		if unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

func isAllCaps(s string) bool {
	hasLetter := false
	for _, r := range s {
		if unicode.IsLetter(r) {
			hasLetter = true
			if !unicode.IsUpper(r) {
				return false
			}
		}
	}
	return hasLetter
}

// levenshteinCutoff returns the edit distance, or cutoff+1 if it exceeds it.
func levenshteinCutoff(a, b []rune, cutoff int) int {
	if abs(len(a)-len(b)) > cutoff {
		return cutoff + 1
	}
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		rowMin := i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min3(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if cur[j] < rowMin {
				rowMin = cur[j]
			}
		}
		if rowMin > cutoff {
			return cutoff + 1
		}
		prev = cur
	}
	return prev[len(b)]
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}

// trailingWord returns the word at the end of d and its start offset.
func trailingWord(d []rune) (string, int) {
	i := len(d)
	for i > 0 {
		r := d[i-1]
		if !unicode.IsLetter(r) && r != '\'' {
			break
		}
		i--
	}
	return string(d[i:]), i
}

func isBoundary(r rune) bool {
	if r == ' ' || r == '\t' || r == '\n' {
		return true
	}
	return strings.ContainsRune(",.!?;:\"()[]{}-–—/\\|@#$%^&*+=~<>", r)
}
