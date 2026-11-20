package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleDiff = `diff --git a/src/limiter.go b/src/limiter.go
index 1111111..2222222 100644
--- a/src/limiter.go
+++ b/src/limiter.go
@@ -8,4 +8,5 @@ func Allow(n int) bool {
 	n++
-	if n > Max {
+	if n >= Max {
+		log.Print("locked")
 		return false
diff --git a/old.go b/old.go
deleted file mode 100644
--- a/old.go
+++ /dev/null
@@ -1,2 +0,0 @@
-package old
-func Gone() {}
`

func TestAnchorFlagsFindingsTheCodeDoesNotSupport(t *testing.T) {
	wt := t.TempDir()
	var src []string
	for i := 1; i <= 20; i++ {
		src = append(src, "// filler")
	}
	src[8], src[9], src[10] = "	n++", "	if n >= Max {", `		log.Print("locked")`
	src[15] = "func helper() {"
	os.MkdirAll(filepath.Join(wt, "src"), 0o755)
	os.WriteFile(filepath.Join(wt, "src/limiter.go"), []byte(strings.Join(src, "\n")), 0o644)
	os.WriteFile(filepath.Join(wt, "README.md"), []byte("hello\nworld\n"), 0o644)
	files := parseDiff(sampleDiff)

	cases := []struct {
		name string
		f    Finding
		want string
		at   int
	}{
		{"cited line is in the diff", Finding{File: "src/limiter.go", Line: 10, Quote: "if n >= Max {"}, "in_diff", 0},
		{"multi-line quote with odd spacing", Finding{File: "src/limiter.go", Line: 10, Quote: "if n >=   Max {\n\n log.Print(\"locked\")"}, "in_diff", 0},
		{"line outside the hunks", Finding{File: "src/limiter.go", Line: 16, Quote: "func helper() {"}, "outside_diff", 0},
		{"quote is far from the cited line but inside the diff", Finding{File: "src/limiter.go", Line: 18, Quote: "if n >= Max {"}, "in_diff", 10},
		{"quote invented", Finding{File: "src/limiter.go", Line: 10, Quote: "if n > Max+1 {"}, "quote_not_found", 0},
		{"old code the diff removed is not at the head", Finding{File: "src/limiter.go", Line: 10, Quote: "if n > Max {"}, "quote_not_found", 0},
		{"file the PR does not touch", Finding{File: "README.md", Line: 1, Quote: "hello"}, "file_not_in_pr", 0},
		{"file that does not exist", Finding{File: "src/nope.go", Line: 3, Quote: "x"}, "file_not_found", 0},
		{"line past the end of the file", Finding{File: "src/limiter.go", Line: 400, Quote: "n++"}, "line_out_of_range", 0},
		{"deleted file, quote from the removed text", Finding{File: "old.go", Line: 2, Quote: "func Gone() {}"}, "in_diff", 0},
		{"deleted file, invented quote", Finding{File: "old.go", Line: 2, Quote: "func Other()"}, "quote_not_found", 0},
		{"no file", Finding{}, "unanchored", 0},
	}
	for _, c := range cases {
		got := anchor(c.f, files, wt)
		if got.Status != c.want || got.QuoteAt != c.at {
			t.Errorf("%s: got %s (quote_at %d, %q), want %s (quote_at %d)", c.name, got.Status, got.QuoteAt, got.Note, c.want, c.at)
		}
	}
}

func TestVerdictCannotApproveOverStandingProblems(t *testing.T) {
	rv := &Review{
		Requirements: []Requirement{{ID: "R1"}, {ID: "R2"}},
		Findings:     []Finding{{ID: "F1", Severity: "blocker"}, {ID: "F2", Severity: "blocker"}},
	}
	tr := &TestReport{Tests: []Test{{ID: "T1"}}}
	base := func() Verdict {
		return Verdict{
			Decision: "approve", Summary: "ok",
			Requirements: []RequirementCheck{{ID: "R1", Status: "met", Evidence: []string{"T1"}}, {ID: "R2", Status: "met", Evidence: []string{"T1"}}},
			Rulings: []Ruling{
				{FindingID: "F1", Ruling: "dismissed_hallucinated", Reason: "no such call"},
				{FindingID: "F2", Ruling: "downgraded", Severity: "minor", Reason: "rare path"},
			},
			Tests: []TestAssessment{{TestID: "T1", Trusted: true, Reason: "asserts it"}},
		}
	}

	if v := base(); v.validate(rv, tr) != nil {
		t.Fatalf("a dismissed blocker and a minor downgrade should allow approve: %v", v.validate(rv, tr))
	}
	cases := map[string]func(*Verdict){
		"downgraded to major still blocks": func(v *Verdict) { v.Rulings[1].Severity = "major" },
		"unverified requirement blocks":    func(v *Verdict) { v.Requirements[1] = RequirementCheck{ID: "R2", Status: "unverified"} },
		"missed major issue blocks":        func(v *Verdict) { v.Missed = []Finding{{ID: "M1", Severity: "major"}} },
		"missing ruling":                   func(v *Verdict) { v.Rulings = v.Rulings[:1] },
		"missing test assessment":          func(v *Verdict) { v.Tests = nil },
		"met without evidence":             func(v *Verdict) { v.Requirements[0].Evidence = nil },
		"downgrade that is not lower":      func(v *Verdict) { v.Decision = "request_changes"; v.Rulings[1].Severity = "blocker" },
	}
	for name, mutate := range cases {
		v := base()
		mutate(&v)
		if v.validate(rv, tr) == nil {
			t.Errorf("%s: approve was accepted", name)
		}
	}
	v := base()
	v.Rulings[1].Severity = "major"
	v.Decision = "request_changes"
	if err := v.validate(rv, tr); err != nil {
		t.Errorf("request_changes over a standing major should be accepted: %v", err)
	}
}

func TestE2ETestNeedsVisualEvidence(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "artifacts"), 0o755)
	for _, f := range []string{"artifacts/out.log", "artifacts/shot.png"} {
		os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644)
	}
	tr := TestReport{
		Summary: "s", Conventions: "c",
		Tests:     []Test{{ID: "T1", Kind: "e2e", Name: "n", Command: "c", Status: "pass", Artifacts: []string{"artifacts/out.log"}}},
		Artifacts: []Artifact{{Path: "artifacts/out.log", Type: "log", TestID: "T1"}},
	}
	if err := tr.validate(dir); err == nil || !strings.Contains(err.Error(), "no video or screenshot") {
		t.Fatalf("an e2e test with only a log should be rejected, got %v", err)
	}
	tr.Tests[0].Artifacts = append(tr.Tests[0].Artifacts, "artifacts/shot.png")
	tr.Artifacts = append(tr.Artifacts, Artifact{Path: "artifacts/shot.png", Type: "screenshot", TestID: "T1"})
	if err := tr.validate(dir); err != nil {
		t.Fatalf("with a screenshot it should pass: %v", err)
	}
	tr.Artifacts[1].Path = "../outside.png"
	tr.Tests[0].Artifacts[1] = "../outside.png"
	if err := tr.validate(dir); err == nil {
		t.Fatal("an artifact outside the run directory should be rejected")
	}
}
