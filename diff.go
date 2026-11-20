package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// DiffFile is one file in a unified diff, by its path at the PR head.
type DiffFile struct {
	Path    string
	Deleted bool
	Lines   map[int]bool // new-side line numbers the diff shows, added or context
	Text    []string     // every line the diff shows for this file, without its +/-/space prefix
}

// parseDiff reads the output of `gh pr diff` or `git diff`.
func parseDiff(diff string) map[string]*DiffFile {
	files := map[string]*DiffFile{}
	var cur *DiffFile
	var oldPath string
	newLine, inHunk := 0, false
	for _, l := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(l, "diff --git "):
			cur, inHunk = nil, false
		case strings.HasPrefix(l, "--- "):
			oldPath = strings.TrimPrefix(strings.TrimPrefix(l, "--- "), "a/")
		case strings.HasPrefix(l, "+++ "):
			p := strings.TrimPrefix(l, "+++ ")
			cur = &DiffFile{Lines: map[int]bool{}}
			if p == "/dev/null" {
				cur.Path, cur.Deleted = oldPath, true
			} else {
				cur.Path = strings.TrimPrefix(p, "b/")
			}
			files[cur.Path] = cur
		case strings.HasPrefix(l, "@@ ") && cur != nil:
			// @@ -a,b +c,d @@
			fields := strings.Fields(l)
			if len(fields) >= 3 {
				n, _, _ := strings.Cut(strings.TrimPrefix(fields[2], "+"), ",")
				newLine, _ = strconv.Atoi(n)
			}
			inHunk = true
		case cur != nil && inHunk:
			switch {
			case strings.HasPrefix(l, "+"):
				cur.Lines[newLine] = true
				cur.Text = append(cur.Text, l[1:])
				newLine++
			case strings.HasPrefix(l, " "):
				cur.Lines[newLine] = true
				cur.Text = append(cur.Text, l[1:])
				newLine++
			case strings.HasPrefix(l, "-"):
				cur.Text = append(cur.Text, l[1:])
			}
		}
	}
	return files
}

// anchor checks a reviewer finding against the diff and the PR head checkout,
// so the judge can see at a glance which findings point at code that exists.
// It does not decide whether a finding is right, only whether it is grounded.
func anchor(f Finding, files map[string]*DiffFile, worktree string) Anchor {
	if f.File == "" {
		return Anchor{Status: "unanchored", Note: "the finding names no file"}
	}
	df := files[f.File]
	var head []string
	if b, err := os.ReadFile(filepath.Join(worktree, filepath.FromSlash(f.File))); err == nil {
		head = strings.Split(string(b), "\n")
	}
	switch {
	case df == nil && head == nil:
		return Anchor{Status: "file_not_found", Note: "no such file in the PR or at its head"}
	case df != nil && df.Deleted:
		if f.Quote != "" && findQuote(df.Text, f.Quote) == 0 {
			return Anchor{Status: "quote_not_found", Note: "the file is deleted by the PR and the diff has no such text"}
		}
		return Anchor{Status: "in_diff", Note: "the file is deleted by the PR"}
	case head != nil && f.Line > len(head):
		return Anchor{Status: "line_out_of_range", Note: fmt.Sprintf("the file has %d lines at the PR head", len(head))}
	}

	a := Anchor{Status: "outside_diff"}
	if df == nil {
		a = Anchor{Status: "file_not_in_pr", Note: "the PR does not change this file"}
	} else if df.Lines[f.Line] {
		a.Status = "in_diff"
	}
	if f.Quote != "" && head != nil {
		at := findQuote(head, f.Quote)
		switch {
		case at == 0:
			return Anchor{Status: "quote_not_found", Note: "the quoted code is not in the file at the PR head"}
		case at < f.Line-3 || at > max(f.EndLine, f.Line)+3:
			a.QuoteAt = at
			a.Note = fmt.Sprintf("the quote is at line %d, not %d", at, f.Line)
			if df != nil && df.Lines[at] {
				a.Status = "in_diff"
			}
		}
	}
	return a
}

// findQuote returns the 1-based line where quote starts in lines, comparing
// with whitespace collapsed, or 0 when it is not there.
func findQuote(lines []string, quote string) int {
	want := squash(quote)
	if want == "" {
		return 0
	}
	// Join with newlines kept as spaces, then map offsets back to lines.
	var b strings.Builder
	starts := make([]int, len(lines))
	for i, l := range lines {
		starts[i] = b.Len()
		if s := squash(l); s != "" { // blank lines vanish, as they do in want
			b.WriteString(s)
			b.WriteByte(' ')
		}
	}
	idx := strings.Index(b.String(), want)
	if idx < 0 {
		return 0
	}
	for i := len(starts) - 1; i >= 0; i-- {
		if starts[i] <= idx {
			return i + 1
		}
	}
	return 1
}

func squash(s string) string { return strings.Join(strings.Fields(s), " ") }
