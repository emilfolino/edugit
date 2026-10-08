package web

import (
	"strconv"
	"strings"
)

// diffFile is one file of a parsed unified diff.
type diffFile struct {
	Path    string
	Status  string // added, deleted or modified
	Binary  bool
	Added   int
	Removed int
	Hunks   []*diffHunk
}

// diffHunk is one @@ section, with its lines also paired up for the split view.
type diffHunk struct {
	Header string
	Lines  []diffLine
	Rows   []splitRow
}

// diffLine is one line of a hunk. Kind is ' ', '+', '-' or '\'.
type diffLine struct {
	Kind byte
	Text string
	Old  int // line number in the old file, 0 if none
	New  int
}

// splitRow pairs an old line with a new one; either side may be empty.
type splitRow struct {
	Left, Right *diffLine
}

// parseDiff parses the output of git diff --no-color --no-renames.
// Unrecognised lines outside hunks are ignored.
func parseDiff(raw string) []diffFile {
	var files []diffFile
	var f *diffFile
	var h *diffHunk
	var oldN, newN int
	flush := func() {
		if h != nil {
			h.Rows = pairRows(h.Lines)
			h = nil
		}
	}
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			flush()
			files = append(files, diffFile{Path: headerPath(line), Status: "modified"})
			f = &files[len(files)-1]
			continue
		}
		if f == nil {
			continue
		}
		if h == nil || !inHunk(line, oldN, newN) {
			switch {
			case strings.HasPrefix(line, "@@ "):
				flush()
				oldN, newN = hunkStart(line)
				h = &diffHunk{Header: line}
				f.Hunks = append(f.Hunks, h)
			case strings.HasPrefix(line, "new file mode"):
				f.Status = "added"
			case strings.HasPrefix(line, "deleted file mode"):
				f.Status = "deleted"
			case strings.HasPrefix(line, "Binary files "), strings.HasPrefix(line, "GIT binary patch"):
				f.Binary = true
			case h == nil && strings.HasPrefix(line, "+++ ") && line != "+++ /dev/null":
				f.Path = unquotePath(strings.TrimPrefix(strings.TrimPrefix(line, "+++ "), "b/"))
			case h == nil && strings.HasPrefix(line, "--- ") && line != "--- /dev/null" && f.Status == "deleted":
				f.Path = unquotePath(strings.TrimPrefix(strings.TrimPrefix(line, "--- "), "a/"))
			}
			continue
		}
		if line == "" {
			continue
		}
		l := diffLine{Kind: line[0], Text: line[1:]}
		switch l.Kind {
		case '+':
			l.New = newN
			newN++
			f.Added++
		case '-':
			l.Old = oldN
			oldN++
			f.Removed++
		case ' ':
			l.Old, l.New = oldN, newN
			oldN++
			newN++
		}
		h.Lines = append(h.Lines, l)
	}
	flush()
	return files
}

// inHunk reports whether line continues the current hunk rather than starting
// a new file or hunk header. A removed line may legitimately read "-- a/x".
func inHunk(line string, _, _ int) bool {
	if line == "" {
		return true
	}
	if strings.HasPrefix(line, "diff --git ") || strings.HasPrefix(line, "@@ ") {
		return false
	}
	switch line[0] {
	case '+', '-', ' ', '\\':
		return true
	}
	return false
}

// hunkStart reads the starting line numbers from "@@ -a,b +c,d @@".
func hunkStart(header string) (oldStart, newStart int) {
	fields := strings.Fields(header)
	if len(fields) < 3 {
		return 1, 1
	}
	return rangeStart(fields[1]), rangeStart(fields[2])
}

func rangeStart(s string) int {
	s = strings.TrimLeft(s, "-+")
	if i := strings.IndexByte(s, ','); i >= 0 {
		s = s[:i]
	}
	n, _ := strconv.Atoi(s)
	return n
}

// headerPath extracts the b-side path of a "diff --git a/x b/x" line.
func headerPath(line string) string {
	rest := strings.TrimPrefix(line, "diff --git ")
	if i := strings.LastIndex(rest, " b/"); i >= 0 {
		return unquotePath(rest[i+3:])
	}
	return unquotePath(rest)
}

func unquotePath(p string) string {
	if u, err := strconv.Unquote(p); err == nil {
		return strings.TrimPrefix(u, "b/")
	}
	return p
}

// pairRows aligns runs of removed and added lines side by side.
func pairRows(lines []diffLine) []splitRow {
	var rows []splitRow
	for i := 0; i < len(lines); {
		switch lines[i].Kind {
		case ' ':
			rows = append(rows, splitRow{Left: &lines[i], Right: &lines[i]})
			i++
		case '\\':
			i++
		default:
			var del, add []*diffLine
			for ; i < len(lines) && (lines[i].Kind == '-' || lines[i].Kind == '+'); i++ {
				if lines[i].Kind == '-' {
					del = append(del, &lines[i])
				} else {
					add = append(add, &lines[i])
				}
			}
			for j := 0; j < len(del) || j < len(add); j++ {
				var row splitRow
				if j < len(del) {
					row.Left = del[j]
				}
				if j < len(add) {
					row.Right = add[j]
				}
				rows = append(rows, row)
			}
		}
	}
	return rows
}

// Class is the CSS class of the line.
func (l diffLine) Class() string {
	switch l.Kind {
	case '+':
		return "add"
	case '-':
		return "del"
	}
	return ""
}
