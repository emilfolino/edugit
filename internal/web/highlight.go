package web

import (
	"html"
	"html/template"
	"path"
	"strings"
)

// lang describes just enough of a language to colour comments, strings,
// numbers and keywords. It is a lexical approximation, not a parser.
type lang struct {
	keywords map[string]bool
	line     []string // line comment openers
	blockO   string   // block comment delimiters, empty if none
	blockC   string
	quotes   string
}

func words(s string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(s) {
		m[w] = true
	}
	return m
}

var (
	cLike = func(kw string) *lang {
		return &lang{keywords: words(kw), line: []string{"//"}, blockO: "/*", blockC: "*/", quotes: "\"'`"}
	}
	langs = map[string]*lang{
		".go":   cLike("break case chan const continue default defer else fallthrough for func go goto if import interface map package range return select struct switch type var true false nil"),
		".js":   cLike("async await break case catch class const continue default delete do else export extends finally for from function if import in instanceof let new of return static super switch this throw try typeof var void while yield true false null undefined"),
		".java": cLike("abstract boolean break byte case catch char class const continue default do double else enum extends final finally float for if implements import instanceof int interface long new package private protected public return short static super switch this throw throws try void volatile while true false null"),
		".c":    cLike("auto break case char const continue default do double else enum extern float for goto if inline int long register return short signed sizeof static struct switch typedef union unsigned void volatile while"),
		".rs":   cLike("as break const continue crate else enum fn for if impl in let loop match mod mut pub ref return self static struct trait type unsafe use where while true false"),
		".css":  {keywords: words(""), blockO: "/*", blockC: "*/", quotes: "\"'"},
		".py":   {keywords: words("and as assert async await break class continue def del elif else except finally for from global if import in is lambda None nonlocal not or pass raise return try while with yield True False"), line: []string{"#"}, quotes: "\"'"},
		".sh":   {keywords: words("case do done elif else esac fi for function if in then until while"), line: []string{"#"}, quotes: "\"'"},
		".sql":  {keywords: words("select from where insert into values update set delete create table index primary key foreign references not null and or order by group having join left right inner on as limit unique default"), line: []string{"--"}, blockO: "/*", blockC: "*/", quotes: "'\""},
	}
)

func init() {
	langs[".ts"] = langs[".js"]
	langs[".mjs"] = langs[".js"]
	langs[".jsx"] = langs[".js"]
	langs[".tsx"] = langs[".js"]
	langs[".h"] = langs[".c"]
	langs[".cpp"] = langs[".c"]
	langs[".cs"] = langs[".java"]
	langs[".kt"] = langs[".java"]
	langs[".bash"] = langs[".sh"]
	langs[".yml"] = &lang{keywords: words("true false null"), line: []string{"#"}, quotes: "\"'"}
	langs[".yaml"] = langs[".yml"]
	langs[".toml"] = langs[".yml"]
}

// highlight returns one HTML string per source line with comment, string,
// number and keyword spans. Unknown file types are only escaped. All output
// is escaped, so it is safe to mark as template.HTML.
func highlight(name, src string) []template.HTML {
	src = strings.TrimSuffix(src, "\n")
	rawLines := strings.Split(src, "\n")
	out := make([]template.HTML, len(rawLines))
	l := langs[strings.ToLower(path.Ext(name))]
	if l == nil {
		for i, ln := range rawLines {
			out[i] = template.HTML(html.EscapeString(ln))
		}
		return out
	}
	inBlock := false
	for i, ln := range rawLines {
		var b strings.Builder
		inBlock = l.lexLine(&b, ln, inBlock)
		out[i] = template.HTML(b.String())
	}
	return out
}

func span(b *strings.Builder, class, text string) {
	b.WriteString(`<span class="` + class + `">` + html.EscapeString(text) + `</span>`)
}

// lexLine writes one line and reports whether a block comment is still open.
func (l *lang) lexLine(b *strings.Builder, s string, inBlock bool) bool {
	i := 0
	for i < len(s) {
		rest := s[i:]
		if inBlock {
			end := strings.Index(rest, l.blockC)
			if end < 0 {
				span(b, "tok-c", rest)
				return true
			}
			span(b, "tok-c", rest[:end+len(l.blockC)])
			i += end + len(l.blockC)
			inBlock = false
			continue
		}
		if l.blockO != "" && strings.HasPrefix(rest, l.blockO) {
			inBlock = true
			span(b, "tok-c", l.blockO)
			i += len(l.blockO)
			continue
		}
		matched := false
		for _, lc := range l.line {
			if strings.HasPrefix(rest, lc) {
				span(b, "tok-c", rest)
				return inBlock
			}
		}
		c := rest[0]
		switch {
		case strings.IndexByte(l.quotes, c) >= 0:
			j := 1
			for j < len(rest) && rest[j] != c {
				if rest[j] == '\\' && c != '`' {
					j++
				}
				j++
			}
			if j < len(rest) {
				j++
			} else {
				j = len(rest)
			}
			span(b, "tok-s", rest[:j])
			i += j
			matched = true
		case c >= '0' && c <= '9':
			j := 1
			for j < len(rest) && (isWord(rest[j]) || rest[j] == '.') {
				j++
			}
			span(b, "tok-n", rest[:j])
			i += j
			matched = true
		case isWord(c):
			j := 1
			for j < len(rest) && isWord(rest[j]) {
				j++
			}
			if l.keywords[rest[:j]] {
				span(b, "tok-k", rest[:j])
			} else {
				b.WriteString(html.EscapeString(rest[:j]))
			}
			i += j
			matched = true
		}
		if !matched {
			// Copy a whole UTF-8 sequence to keep it valid.
			j := 1
			for j < len(rest) && rest[j]&0xC0 == 0x80 {
				j++
			}
			b.WriteString(html.EscapeString(rest[:j]))
			i += j
		}
	}
	return inBlock
}

func isWord(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c >= 0x80
}
