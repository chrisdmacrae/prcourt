package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// The three files the agents hand back through `prcourt submit`. Each one is
// validated before it is accepted, so the judge and the TUI can rely on its
// shape. See prompts/*.md for how each role is told to fill them in.

type Requirement struct {
	ID     string `json:"id"`
	Text   string `json:"text"`
	Source string `json:"source"` // where it came from: description, issue #12, review comment, ...
}

type Anchor struct {
	// in_diff, outside_diff, file_not_in_pr, file_not_found,
	// line_out_of_range, quote_not_found, or unanchored.
	Status  string `json:"status"`
	QuoteAt int    `json:"quote_at,omitempty"` // the line the quote was found on, when it differs from Line
	Note    string `json:"note,omitempty"`
}

type Finding struct {
	ID         string  `json:"id"`
	Severity   string  `json:"severity"` // blocker, major, minor, nit
	Category   string  `json:"category"`
	File       string  `json:"file,omitempty"`
	Line       int     `json:"line,omitempty"`
	EndLine    int     `json:"end_line,omitempty"`
	Quote      string  `json:"quote,omitempty"`
	Title      string  `json:"title"`
	Body       string  `json:"body"`
	Suggestion string  `json:"suggestion,omitempty"`
	Evidence   string  `json:"evidence"`
	Anchor     *Anchor `json:"anchor,omitempty"` // set by prcourt on submit
}

type ThreadNote struct {
	URL    string `json:"url"`
	Status string `json:"status"` // addressed, unaddressed, outdated, question
	Note   string `json:"note"`
}

type Review struct {
	Summary      string        `json:"summary"`
	Requirements []Requirement `json:"requirements"`
	Findings     []Finding     `json:"findings"`
	Threads      []ThreadNote  `json:"comment_threads,omitempty"`
}

type Test struct {
	ID         string   `json:"id"`
	Kind       string   `json:"kind"` // unit, integration, contract, e2e
	Name       string   `json:"name"`
	Command    string   `json:"command"`
	Status     string   `json:"status"` // pass, fail, skip, error
	Covers     []string `json:"covers"` // the PR behaviors it checks, in words; the judge maps them to requirements
	AddedFiles []string `json:"added_files,omitempty"`
	Artifacts  []string `json:"artifacts"` // paths relative to the run directory
	Notes      string   `json:"notes,omitempty"`
}

type Artifact struct {
	Path    string   `json:"path"`
	Type    string   `json:"type"` // video, screenshot, log, trace
	TestID  string   `json:"test_id"`
	Caption string   `json:"caption,omitempty"`
	Frames  []string `json:"frames,omitempty"` // set by prcourt on submit for videos
}

type TestReport struct {
	Summary     string     `json:"summary"`
	Conventions string     `json:"conventions"`
	Tests       []Test     `json:"tests"`
	Artifacts   []Artifact `json:"artifacts"`
}

type RequirementCheck struct {
	ID       string   `json:"id"`
	Status   string   `json:"status"` // met, unmet, unverified
	Evidence []string `json:"evidence"`
	Reason   string   `json:"reason"`
}

type Ruling struct {
	FindingID string `json:"finding_id"`
	// upheld, downgraded, dismissed_pedantic, dismissed_hallucinated, dismissed_wrong
	Ruling   string `json:"ruling"`
	Severity string `json:"severity,omitempty"` // the severity the judge settles on, for upheld and downgraded
	Reason   string `json:"reason"`
}

type TestAssessment struct {
	TestID  string `json:"test_id"`
	Trusted bool   `json:"trusted"`
	Reason  string `json:"reason"`
}

type Verdict struct {
	Decision     string             `json:"decision"` // approve, request_changes, needs_human
	Summary      string             `json:"summary"`
	Requirements []RequirementCheck `json:"requirements"`
	Rulings      []Ruling           `json:"rulings"`
	Tests        []TestAssessment   `json:"test_assessment"`
	Missed       []Finding          `json:"missed_issues,omitempty"` // problems the reviewer missed, found by the judge
}

var (
	severities  = []string{"blocker", "major", "minor", "nit"}
	categories  = []string{"correctness", "security", "performance", "contract", "tests", "design", "docs", "convention"}
	testKinds   = []string{"unit", "integration", "contract", "e2e"}
	testStates  = []string{"pass", "fail", "skip", "error"}
	artTypes    = []string{"video", "screenshot", "log", "trace"}
	reqStates   = []string{"met", "unmet", "unverified"}
	rulingKinds = []string{"upheld", "downgraded", "dismissed_pedantic", "dismissed_hallucinated", "dismissed_wrong"}
	decisions   = []string{"approve", "request_changes", "needs_human"}
)

// problems collects every validation failure so an agent can fix them all in
// one pass instead of resubmitting once per mistake.
type problems []string

func (p *problems) addf(format string, a ...any) { *p = append(*p, fmt.Sprintf(format, a...)) }

func (p problems) err() error {
	if len(p) == 0 {
		return nil
	}
	return fmt.Errorf("%d problem(s):\n  - %s", len(p), strings.Join(p, "\n  - "))
}

func oneOf(p *problems, what, v string, allowed []string) {
	if !slices.Contains(allowed, v) {
		p.addf("%s is %q; use one of %s", what, v, strings.Join(allowed, ", "))
	}
}

func uniqueIDs[T any](p *problems, what string, items []T, id func(T) string) map[string]bool {
	seen := map[string]bool{}
	for i, it := range items {
		k := id(it)
		switch {
		case k == "":
			p.addf("%s[%d] has no id", what, i)
		case seen[k]:
			p.addf("%s id %q is used twice", what, k)
		}
		seen[k] = true
	}
	return seen
}

func (r *Review) validate() error {
	var p problems
	if strings.TrimSpace(r.Summary) == "" {
		p.addf("summary is empty")
	}
	if len(r.Requirements) == 0 {
		p.addf("requirements is empty; list what the PR must do, from its description, linked issues, and review comments")
	}
	uniqueIDs(&p, "requirements", r.Requirements, func(q Requirement) string { return q.ID })
	uniqueIDs(&p, "findings", r.Findings, func(f Finding) string { return f.ID })
	for _, f := range r.Findings {
		oneOf(&p, "finding "+f.ID+" severity", f.Severity, severities)
		oneOf(&p, "finding "+f.ID+" category", f.Category, categories)
		if f.Title == "" || f.Body == "" {
			p.addf("finding %s needs a title and a body", f.ID)
		}
		if strings.TrimSpace(f.Evidence) == "" {
			p.addf("finding %s has no evidence; say what you read or ran that shows the problem", f.ID)
		}
		if f.File != "" && f.Line <= 0 {
			p.addf("finding %s names %s but no line", f.ID, f.File)
		}
		if f.File != "" && f.Quote == "" {
			p.addf("finding %s has no quote; copy the exact code it is about from the PR head", f.ID)
		}
	}
	return p.err()
}

// validate checks the report against the run directory: every artifact must
// exist inside it, and every e2e test must have a video or a screenshot.
func (t *TestReport) validate(runDir string) error {
	var p problems
	if strings.TrimSpace(t.Summary) == "" {
		p.addf("summary is empty")
	}
	if strings.TrimSpace(t.Conventions) == "" {
		p.addf("conventions is empty; describe the test frameworks, file layout, and commands the repo uses")
	}
	if len(t.Tests) == 0 {
		p.addf("tests is empty")
	}
	tests := uniqueIDs(&p, "tests", t.Tests, func(x Test) string { return x.ID })
	byPath := map[string]Artifact{}
	for _, a := range t.Artifacts {
		oneOf(&p, "artifact "+a.Path+" type", a.Type, artTypes)
		if !tests[a.TestID] {
			p.addf("artifact %s belongs to test %q, which is not in tests", a.Path, a.TestID)
		}
		if err := insideRun(runDir, a.Path); err != nil {
			p.addf("artifact %s: %v", a.Path, err)
		}
		byPath[a.Path] = a
	}
	for _, x := range t.Tests {
		oneOf(&p, "test "+x.ID+" kind", x.Kind, testKinds)
		oneOf(&p, "test "+x.ID+" status", x.Status, testStates)
		if x.Command == "" {
			p.addf("test %s has no command; give the exact command that runs it", x.ID)
		}
		var visual, log bool
		for _, path := range x.Artifacts {
			a, ok := byPath[path]
			if !ok {
				p.addf("test %s lists artifact %s, which is not in artifacts", x.ID, path)
				continue
			}
			visual = visual || a.Type == "video" || a.Type == "screenshot"
			log = log || a.Type == "log"
		}
		if !log {
			p.addf("test %s has no log artifact; save its command output under artifacts/", x.ID)
		}
		if x.Kind == "e2e" && x.Status != "skip" && !visual {
			p.addf("e2e test %s has no video or screenshot; record the browser session", x.ID)
		}
	}
	return p.err()
}

// validate checks the verdict against what the judge was given: every finding
// gets a ruling, every requirement a status, every test an assessment, and the
// decision must follow from them.
func (v *Verdict) validate(r *Review, t *TestReport) error {
	var p problems
	oneOf(&p, "decision", v.Decision, decisions)
	if strings.TrimSpace(v.Summary) == "" {
		p.addf("summary is empty")
	}
	ruled := map[string]Ruling{}
	for _, x := range v.Rulings {
		oneOf(&p, "ruling for "+x.FindingID, x.Ruling, rulingKinds)
		if x.Ruling == "upheld" || x.Ruling == "downgraded" {
			oneOf(&p, "ruling for "+x.FindingID+" severity", x.Severity, severities)
		}
		if x.Reason == "" {
			p.addf("ruling for %s has no reason", x.FindingID)
		}
		ruled[x.FindingID] = x
	}
	blocking := 0
	for _, f := range r.Findings {
		x, ok := ruled[f.ID]
		if !ok {
			p.addf("finding %s has no ruling", f.ID)
		}
		if x.Ruling == "downgraded" && slices.Index(severities, x.Severity) <= slices.Index(severities, f.Severity) {
			p.addf("finding %s is downgraded to %s, which is not below its %s; uphold it or pick a lower severity", f.ID, x.Severity, f.Severity)
		}
		if (x.Ruling == "upheld" || x.Ruling == "downgraded") && (x.Severity == "blocker" || x.Severity == "major") {
			blocking++
		}
	}
	for _, m := range v.Missed {
		oneOf(&p, "missed issue "+m.ID+" severity", m.Severity, severities)
		if m.Severity == "blocker" || m.Severity == "major" {
			blocking++
		}
	}
	checked := map[string]RequirementCheck{}
	for _, c := range v.Requirements {
		oneOf(&p, "requirement "+c.ID+" status", c.Status, reqStates)
		if len(c.Evidence) == 0 && c.Status != "unverified" {
			p.addf("requirement %s is %s without evidence; cite test ids, artifact or frame paths, or file:line", c.ID, c.Status)
		}
		checked[c.ID] = c
	}
	unmet := 0
	for _, q := range r.Requirements {
		c, ok := checked[q.ID]
		if !ok {
			p.addf("requirement %s has no status", q.ID)
		}
		if c.Status != "met" {
			unmet++
		}
	}
	assessed := map[string]bool{}
	for _, a := range v.Tests {
		assessed[a.TestID] = true
	}
	for _, x := range t.Tests {
		if !assessed[x.ID] {
			p.addf("test %s has no assessment", x.ID)
		}
	}
	if v.Decision == "approve" && (blocking > 0 || unmet > 0) {
		p.addf("decision is approve, but %d blocker or major issue(s) stand and %d requirement(s) are not met", blocking, unmet)
	}
	return p.err()
}

// insideRun reports an error unless rel names an existing file inside runDir.
func insideRun(runDir, rel string) error {
	if filepath.IsAbs(rel) {
		return fmt.Errorf("use a path relative to the run directory")
	}
	abs := filepath.Join(runDir, rel)
	if r, err := filepath.Rel(runDir, abs); err != nil || strings.HasPrefix(r, "..") {
		return fmt.Errorf("is outside the run directory")
	}
	if fi, err := os.Stat(abs); err != nil || fi.IsDir() {
		return fmt.Errorf("does not exist")
	}
	return nil
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
