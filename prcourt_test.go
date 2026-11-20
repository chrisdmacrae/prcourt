package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseInstaller(t *testing.T) {
	cases := []struct {
		name, os, arch, asset, installed, failure string
	}{
		{"macOS x64 install", "Darwin", "x86_64", "prcourt_darwin_amd64", "", ""},
		{"macOS arm64 update", "Darwin", "arm64", "prcourt_darwin_arm64", "v1.0.0", ""},
		{"Linux x64 install", "Linux", "x86_64", "prcourt_linux_amd64", "", ""},
		{"Linux arm64 install", "Linux", "aarch64", "prcourt_linux_arm64", "", ""},
		{"up to date", "Darwin", "arm64", "prcourt_darwin_arm64", "v1.2.3", ""},
		{"bad checksum preserves CLI", "Darwin", "arm64", "prcourt_darwin_arm64", "v1.0.0", "checksum"},
		{"wrong version preserves CLI", "Darwin", "arm64", "prcourt_darwin_arm64", "v1.0.0", "version"},
		{"network failure preserves CLI", "Darwin", "arm64", "prcourt_darwin_arm64", "v1.0.0", "network"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			bin := filepath.Join(home, ".local", "bin")
			if err := os.MkdirAll(bin, 0o755); err != nil {
				t.Fatal(err)
			}
			write := func(path, data string) {
				t.Helper()
				if err := os.WriteFile(path, []byte(data), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			write(filepath.Join(bin, "uname"), fmt.Sprintf("#!/bin/sh\ncase \"$1\" in -s) echo %s ;; -m) echo %s ;; esac\n", c.os, c.arch))
			fixture := t.TempDir()
			version := "v1.2.3"
			if c.failure == "version" {
				version = "v9.9.9"
			}
			binary := fmt.Sprintf("#!/bin/sh\necho %s\n", version)
			write(filepath.Join(fixture, c.asset), binary)
			digest := fmt.Sprintf("%x", sha256.Sum256([]byte(binary)))
			if c.failure == "checksum" {
				digest = strings.Repeat("0", 64)
			}
			write(filepath.Join(fixture, "checksums.txt"), digest+"  "+c.asset+"\n")
			write(filepath.Join(bin, "gh"), `#!/bin/sh
echo "$*" >> "$HOME/requests"
if [ "$1" = api ]; then
  [ "$FAILURE" != network ] || exit 1
  echo v1.2.3
else
  while [ "$1" != --dir ]; do shift; done
  /bin/cp "$FIXTURE"/* "$2/"
fi
`)
			target := filepath.Join(bin, "prcourt")
			old := "#!/bin/sh\necho " + c.installed + "\n"
			if c.installed != "" {
				write(target, old)
			}
			t.Setenv("HOME", home)
			t.Setenv("PATH", bin+":/usr/bin:/bin")
			t.Setenv("FIXTURE", fixture)
			t.Setenv("FAILURE", c.failure)
			out, err := exec.Command("sh", "skill/prcourt/scripts/install.sh").CombinedOutput()
			if (err != nil) != (c.failure != "") {
				t.Fatalf("installer error = %v: %s", err, out)
			}
			got, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			want := binary
			if c.failure != "" || c.installed == "v1.2.3" {
				want = old
			}
			if string(got) != want {
				t.Fatalf("installed bytes = %q, want %q", got, want)
			}
			requests, _ := os.ReadFile(filepath.Join(home, "requests"))
			if c.installed == "v1.2.3" {
				if strings.Contains(string(requests), "download") {
					t.Fatal("up-to-date CLI was downloaded again")
				}
			} else if c.failure != "network" && !strings.Contains(string(requests), "--pattern "+c.asset+" ") {
				t.Fatalf("wrong platform asset requested: %s", requests)
			}
			leftovers, _ := filepath.Glob(filepath.Join(bin, ".prcourt-*"))
			if len(leftovers) != 0 {
				t.Fatalf("installer left temporary files: %v", leftovers)
			}
		})
	}
}

func TestRunSelectsAvailableTerminalTools(t *testing.T) {
	cases := []struct {
		name, rex                               string
		browser, requestBrowser, noRex, wantRex bool
	}{
		{name: "missing Rex falls back", requestBrowser: true},
		{name: "unreachable Rex falls back", rex: "down", browser: true, requestBrowser: true},
		{name: "available Rex opens panes", rex: "up", browser: true, requestBrowser: true, wantRex: true},
		{name: "explicit no Rex skips server", rex: "up", noRex: true},
		{name: "non terminal host leaves browser unused", browser: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bin := t.TempDir()
			stub := func(name, script string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for _, name := range []string{"ffmpeg", "ffprobe", "claude"} {
				stub(name, "exit 0")
			}
			stub("gh", `case "$1 $2" in
"repo view") echo '{"nameWithOwner":"owner/repo"}' ;;
"pr view") echo '{"number":123,"title":"Tool selection","headRefOid":"abcdef123","headRefName":"feature","baseRefName":"main"}' ;;
"api "*) echo '[]' ;;
esac`)
			stub("git", `case "$1 $2" in
"worktree add") /bin/mkdir -p "$4" ;;
"rev-parse --show-toplevel") pwd ;;
esac`)
			calls := filepath.Join(t.TempDir(), "rex-calls")
			t.Setenv("REX_CALLS", calls)
			t.Setenv("REX_SESSION", "")
			if c.rex != "" {
				body := `echo "$1" >> "$REX_CALLS"
case "$1" in
whoami) exit 0 ;;
new) echo '{"session_id":"session","initial_windows":[{"window_id":"window","block_ids":["tui"]}]}' ;;
split) echo '{"block_ids":["agent"]}' ;;
*) exit 1 ;;
esac`
				if c.rex == "down" {
					body = "echo whoami >> \"$REX_CALLS\"; exit 1"
				}
				stub("rex", body)
			}
			if c.browser {
				stub("terminal-browser", "exit 0")
			}
			t.Setenv("PATH", bin)
			args := []string{"-json", "-repo-dir", t.TempDir(), "-out", t.TempDir()}
			if c.requestBrowser {
				args = append(args, "-terminal-browser")
			}
			if c.noRex {
				args = append(args, "-no-rex")
			}
			args = append(args, "123")
			stdout := os.Stdout
			rd, wr, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			os.Stdout = wr
			err = cmdRun(args)
			os.Stdout = stdout
			wr.Close()
			out, readErr := io.ReadAll(rd)
			rd.Close()
			if err != nil || readErr != nil {
				t.Fatalf("run: %v; read: %v; output: %s", err, readErr, out)
			}
			var r Run
			if err := json.Unmarshal(out, &r); err != nil {
				t.Fatalf("run must print only JSON: %v: %s", err, out)
			}
			if (r.Rex.Session != "") != c.wantRex {
				t.Fatalf("Rex session = %q, want Rex = %v", r.Rex.Session, c.wantRex)
			}
			wantBrowser := c.requestBrowser && c.browser
			if r.TerminalBrowser != wantBrowser {
				t.Fatalf("terminal_browser = %v, want %v", r.TerminalBrowser, wantBrowser)
			}
			prompt, err := os.ReadFile(r.path("prompts", "tester.md"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(prompt), "terminal-browser open URL --split right") != wantBrowser {
				t.Fatal("tester instructions do not match selected browser")
			}
			invocations, _ := os.ReadFile(calls)
			if c.noRex && len(invocations) > 0 {
				t.Fatalf("-no-rex invoked Rex: %s", invocations)
			}
			if !c.wantRex && strings.Contains(string(invocations), "new") {
				t.Fatalf("unavailable Rex launched panes: %s", invocations)
			}
			if c.wantRex && (r.Rex.Blocks["reviewer"] == "" || r.Rex.Blocks["tester"] == "") {
				t.Fatal("available Rex did not launch both agents")
			}
		})
	}
}

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
