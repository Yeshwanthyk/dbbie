package ui

import (
	"strings"
	"unicode"

	"github.com/rivo/tview"
)

// The query pane shows SQL for reading, not editing: one clause per line and
// light syntax colour. The tab's SQL text itself is never rewritten.

// clauseStarts are top-level keywords that begin a new display line. Multi-word
// entries match consecutive words, so "order by" breaks but "order" alone does not.
var clauseStarts = [][]string{
	{"select"}, {"from"}, {"where"}, {"group", "by"}, {"order", "by"}, {"having"},
	{"limit"}, {"offset"}, {"union"}, {"intersect"}, {"except"}, {"returning"},
	{"values"}, {"set"}, {"window"},
	{"join"}, {"inner", "join"}, {"left", "join"}, {"right", "join"}, {"full", "join"}, {"cross", "join"},
	{"left", "outer", "join"}, {"right", "outer", "join"}, {"full", "outer", "join"},
}

var sqlKeywords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`select from where group by order having limit offset union all
		intersect except returning values set window join inner left right full outer cross on using
		as and or not in is null like ilike between case when then else end distinct asc desc with
		insert into update delete exists any true false nulls first last over partition filter
		recursive lateral explain analyze show describe`) {
		sqlKeywords[w] = true
	}
}

type sqlToken struct {
	text string
	kind byte // 'w' word, 's' string, 'n' number, 'c' comment, ' ' space, 'p' punctuation
}

func lexSQL(sql string) []sqlToken {
	var out []sqlToken
	r := []rune(sql)
	for i := 0; i < len(r); {
		start := i
		c := r[i]
		switch {
		case unicode.IsSpace(c):
			for i < len(r) && unicode.IsSpace(r[i]) {
				i++
			}
			out = append(out, sqlToken{string(r[start:i]), ' '})
		case c == '\'' || c == '"' || c == '`':
			i++
			for i < len(r) {
				if r[i] == c {
					if i+1 < len(r) && r[i+1] == c { // doubled quote escape
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
			kind := byte('s')
			if c != '\'' {
				kind = 'w' // quoted identifier
			}
			out = append(out, sqlToken{string(r[start:i]), kind})
		case c == '-' && i+1 < len(r) && r[i+1] == '-':
			for i < len(r) && r[i] != '\n' {
				i++
			}
			out = append(out, sqlToken{string(r[start:i]), 'c'})
		case unicode.IsDigit(c):
			for i < len(r) && (unicode.IsDigit(r[i]) || r[i] == '.') {
				i++
			}
			out = append(out, sqlToken{string(r[start:i]), 'n'})
		case unicode.IsLetter(c) || c == '_':
			for i < len(r) && (unicode.IsLetter(r[i]) || unicode.IsDigit(r[i]) || r[i] == '_' || r[i] == '$') {
				i++
			}
			out = append(out, sqlToken{string(r[start:i]), 'w'})
		default:
			i++
			out = append(out, sqlToken{string(c), 'p'})
		}
	}
	return out
}

// layoutSQL returns plain display text: single-line SQL gets a break before
// each top-level clause. SQL that already has newlines keeps its own layout.
func layoutSQL(sql string) string {
	sql = strings.TrimSpace(sql)
	if strings.Contains(sql, "\n") {
		return sql
	}
	tokens := lexSQL(sql)
	var b strings.Builder
	depth := 0
	for i := 0; i < len(tokens); i++ {
		t := tokens[i]
		switch t.text {
		case "(":
			depth++
		case ")":
			depth = max(0, depth-1)
		}
		if t.kind == 'w' && depth == 0 && b.Len() > 0 {
			if n := matchClause(tokens, i); n > 0 {
				out := strings.TrimRight(b.String(), " \t")
				b.Reset()
				b.WriteString(out)
				b.WriteByte('\n')
				for j := i; j < i+n; j++ {
					b.WriteString(tokens[j].text)
				}
				i += n - 1
				continue
			}
		}
		b.WriteString(t.text)
	}
	return b.String()
}

// matchClause reports how many tokens (words plus spaces) the clause at i spans.
func matchClause(tokens []sqlToken, i int) int {
	best := 0
	for _, words := range clauseStarts {
		j, ok := i, true
		for k, w := range words {
			if k > 0 {
				if j >= len(tokens) || tokens[j].kind != ' ' {
					ok = false
					break
				}
				j++
			}
			if j >= len(tokens) || !strings.EqualFold(tokens[j].text, w) {
				ok = false
				break
			}
			j++
		}
		if ok && j-i > best {
			best = j - i
		}
	}
	return best
}

// highlightSQL colours display text with tview tags.
func (s *state) highlightSQL(text string) string {
	var b, plain strings.Builder
	flush := func() { b.WriteString(tview.Escape(plain.String())); plain.Reset() }
	for _, t := range lexSQL(text) {
		color := ""
		switch t.kind {
		case 'w':
			if sqlKeywords[strings.ToLower(t.text)] {
				color = s.theme.Primary
			}
		case 's':
			color = s.theme.Success
		case 'n':
			color = s.theme.Accent
		case 'c':
			color = s.theme.Muted
		case 'p':
			if strings.ContainsAny(t.text, "(),;:") {
				color = s.theme.Muted
			}
		}
		if color == "" {
			plain.WriteString(t.text)
			continue
		}
		flush()
		b.WriteString("[" + color + "]" + tview.Escape(t.text) + "[-]")
	}
	flush()
	return b.String()
}

func (s *state) renderSQL(sql string) string { return s.highlightSQL(layoutSQL(sql)) }

// queryTables lists the relations a statement reads or writes, in order of
// first appearance: names after FROM, JOIN, UPDATE and INTO, including
// comma-separated FROM lists. Subqueries are skipped; their own FROM counts.
func queryTables(sql string) []string {
	var words []sqlToken
	for _, t := range lexSQL(sql) {
		if t.kind != ' ' && t.kind != 'c' {
			words = append(words, t)
		}
	}
	var out []string
	seen := map[string]bool{}
	name := func(i int) (string, int) {
		var b strings.Builder
		for i < len(words) && words[i].kind == 'w' {
			b.WriteString(words[i].text)
			if i+1 < len(words) && words[i+1].text == "." {
				b.WriteByte('.')
				i += 2
				continue
			}
			return b.String(), i + 1
		}
		return "", i
	}
	for i := 0; i < len(words); i++ {
		kw := strings.ToLower(words[i].text)
		if words[i].kind != 'w' || (kw != "from" && kw != "join" && kw != "update" && kw != "into") {
			continue
		}
		for j := i + 1; ; {
			n, next := name(j)
			if n == "" || sqlKeywords[strings.ToLower(n)] {
				break
			}
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
			if kw != "from" {
				break
			}
			// Skip an alias ("t" or "as t"), then continue on a comma list.
			for next < len(words) && words[next].kind == 'w' && !isClauseWord(words[next].text) {
				next++
			}
			if next >= len(words) || words[next].text != "," {
				break
			}
			j = next + 1
		}
	}
	return out
}

func isClauseWord(w string) bool {
	switch strings.ToLower(w) {
	case "where", "group", "order", "having", "limit", "offset", "union", "intersect", "except",
		"returning", "window", "join", "inner", "left", "right", "full", "cross", "on", "using", "natural", "lateral":
		return true
	}
	return false
}

// wrapSQL breaks display text at whitespace only, so operators like ->> and
// quoted strings never split. Continuation lines get a two-space indent; a
// token wider than the line is left for the view's own wrap.
func wrapSQL(text string, width int) string {
	if width < 8 {
		return text
	}
	var out []string
	for _, line := range strings.Split(text, "\n") {
		// Join each run of non-space tokens into one unbreakable word.
		var words []string
		var word strings.Builder
		for _, t := range lexSQL(line) {
			if t.kind == ' ' {
				if word.Len() > 0 {
					words = append(words, word.String())
					word.Reset()
				}
				continue
			}
			word.WriteString(t.text)
		}
		if word.Len() > 0 {
			words = append(words, word.String())
		}
		cur, curWidth := "", 0
		for _, w := range words {
			ww := tview.TaggedStringWidth(tview.Escape(w))
			switch {
			case cur == "":
				cur, curWidth = w, ww
			case curWidth+1+ww > width && strings.TrimSpace(cur) != "":
				out = append(out, cur)
				cur, curWidth = "  "+w, 2+ww
			default:
				cur, curWidth = cur+" "+w, curWidth+1+ww
			}
		}
		out = append(out, cur)
	}
	return strings.Join(out, "\n")
}
