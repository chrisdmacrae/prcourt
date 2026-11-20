package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

var outputs = map[string]string{"reviewer": "review.json", "tester": "tests.json", "judge": "verdict.json"}

// cmdSubmit validates an agent's draft and, when it is valid, publishes it to
// the run. Errors are written for the agent to read and fix, all at once.
func cmdSubmit(args []string) error {
	fs := flag.NewFlagSet("submit", flag.ExitOnError)
	runDir := fs.String("run", "", "run directory (default $PRCOURT_RUN)")
	force := fs.Bool("force", false, "accept findings prcourt cannot find in the code, or resubmit after the judge started")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: prcourt submit [flags] ROLE [DRAFT]\n\nROLE is reviewer, tester, or judge. DRAFT defaults to drafts/<output> in the run.")
		fs.PrintDefaults()
	}
	fs.Parse(reorder(args))
	role := fs.Arg(0)
	if role == "" {
		role = os.Getenv("PRCOURT_ROLE")
	}
	out, ok := outputs[role]
	if !ok {
		fs.Usage()
		os.Exit(2)
	}
	r, err := loadRun(*runDir)
	if err != nil {
		return err
	}
	draft := fs.Arg(1)
	if draft == "" {
		draft = r.path("drafts", out)
	}
	if role != "judge" && exists(r.path("judge.started")) && !*force {
		return errors.New("the judge has already started on the earlier submission; pass -force to replace it anyway")
	}

	switch role {
	case "reviewer":
		var rv Review
		if err := readStrict(draft, &rv); err != nil {
			return err
		}
		if err := rv.validate(); err != nil {
			return err
		}
		diff, _ := os.ReadFile(r.path("pr.diff"))
		files := parseDiff(string(diff))
		var p problems
		for i, f := range rv.Findings {
			a := anchor(f, files, r.Worktree)
			rv.Findings[i].Anchor = &a
			switch a.Status {
			case "file_not_found", "line_out_of_range", "quote_not_found":
				p.addf("finding %s (%s:%d) is %s: %s", f.ID, f.File, f.Line, a.Status, a.Note)
			}
		}
		if len(p) > 0 && !*force {
			return fmt.Errorf("%w\nFix the file, line, or quote of each, or drop the finding. Pass -force only if you are certain the code is there.", p.err())
		}
		if err := writeJSON(r.path(out), rv); err != nil {
			return err
		}
		logEvent(r, "reviewer submitted %d findings and %d requirements", len(rv.Findings), len(rv.Requirements))
		fmt.Printf("Accepted %d findings and %d requirements.\n", len(rv.Findings), len(rv.Requirements))
		for _, f := range rv.Findings {
			if f.Anchor.Status != "in_diff" {
				fmt.Printf("  note: %s is %s. %s\n", f.ID, f.Anchor.Status, f.Anchor.Note)
			}
		}

	case "tester":
		var tr TestReport
		if err := readStrict(draft, &tr); err != nil {
			return err
		}
		if err := tr.validate(r.Dir); err != nil {
			return err
		}
		for i, a := range tr.Artifacts {
			if a.Type != "video" {
				continue
			}
			frames, err := extractFrames(r, a.Path, 12)
			if err != nil {
				return fmt.Errorf("extracting frames from %s: %w", a.Path, err)
			}
			tr.Artifacts[i].Frames = frames
		}
		if err := writeTestPatch(r); err != nil {
			fmt.Fprintln(os.Stderr, "warning: could not save tests.patch:", err)
		}
		if err := writeJSON(r.path(out), tr); err != nil {
			return err
		}
		pass := 0
		for _, t := range tr.Tests {
			if t.Status == "pass" {
				pass++
			}
		}
		logEvent(r, "tester submitted %d tests (%d pass) and %d artifacts", len(tr.Tests), pass, len(tr.Artifacts))
		fmt.Printf("Accepted %d tests (%d pass) and %d artifacts.\n", len(tr.Tests), pass, len(tr.Artifacts))

	case "judge":
		var rv Review
		var tr TestReport
		if err := readJSON(r.path("review.json"), &rv); err != nil {
			return fmt.Errorf("the reviewer has not submitted: %w", err)
		}
		if err := readJSON(r.path("tests.json"), &tr); err != nil {
			return fmt.Errorf("the tester has not submitted: %w", err)
		}
		var v Verdict
		if err := readStrict(draft, &v); err != nil {
			return err
		}
		if err := v.validate(&rv, &tr); err != nil {
			return err
		}
		if err := writeJSON(r.path(out), v); err != nil {
			return err
		}
		if err := os.WriteFile(r.path("verdict.md"), []byte(renderReport(r, &rv, &tr, &v)), 0o644); err != nil {
			return err
		}
		logEvent(r, "judge decided %s", v.Decision)
		fmt.Printf("Accepted the verdict: %s. The report is %s.\n", v.Decision, r.path("verdict.md"))
		return nil
	}
	return maybeStartJudge(r)
}

// maybeStartJudge starts the judge once the reviewer and the tester have both
// submitted. judge.started is created exclusively, so two submissions landing
// together start one judge.
func maybeStartJudge(r *Run) error {
	if !exists(r.path("review.json")) || !exists(r.path("tests.json")) {
		fmt.Println("The judge starts when the other agent submits too.")
		return nil
	}
	f, err := os.OpenFile(r.path("judge.started"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if errors.Is(err, os.ErrExist) {
		return nil
	} else if err != nil {
		return err
	}
	f.Close()
	if r.Rex.Session == "" {
		argv, _ := agentArgv(r, "judge")
		fmt.Printf("Both submitted. Start the judge in %s with:\n  %s\n", r.Worktree, shellJoin(argv))
		return nil
	}
	if err := startAgent(context.Background(), r, "judge", r.Rex.Blocks["reviewer"], "right", 50); err != nil {
		os.Remove(r.path("judge.started"))
		return err
	}
	fmt.Println("Both submitted; the judge has started.")
	return nil
}

// cmdJudge starts the judge by hand, such as when one agent failed to submit.
func cmdJudge(args []string) error {
	r, err := loadRun(first(args))
	if err != nil {
		return err
	}
	os.Remove(r.path("judge.started"))
	return maybeStartJudge(r)
}

// readStrict rejects unknown fields, so a misspelled key fails loudly instead
// of silently dropping what the agent wrote.
func readStrict(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// writeTestPatch saves the tests the tester added to the checkout as a patch
// people can apply to the PR branch.
func writeTestPatch(r *Run) error {
	ctx := context.Background()
	if _, err := runIn(ctx, r.Worktree, "git", "add", "-A", "--intent-to-add"); err != nil {
		return err
	}
	patch, err := runIn(ctx, r.Worktree, "git", "diff", "HEAD")
	if err != nil {
		return err
	}
	return os.WriteFile(r.path("tests.patch"), patch, 0o644)
}

// extractFrames samples n frames evenly from a video, plus its last frame,
// which usually shows the state the test ended in. Paths are relative to the run.
func extractFrames(r *Run, video string, n int) ([]string, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, errors.New("ffmpeg is not installed; install it (brew install ffmpeg) so the judge can see the video")
	}
	ctx := context.Background()
	src := r.path(video)
	// Name the folder after the video's whole path, as tests often all save
	// video.webm in their own folder: artifacts/T6/video.webm -> T6-video.
	name := strings.TrimSuffix(filepath.ToSlash(filepath.Clean(video)), filepath.Ext(video))
	name = strings.ReplaceAll(strings.TrimPrefix(name, "artifacts/"), "/", "-")
	dir := r.path("frames", name)
	os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	dur := 0.0
	if b, err := runIn(ctx, "", "ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", src); err == nil {
		dur, _ = strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
	}
	fps := "1"
	if dur > 0 {
		fps = strconv.FormatFloat(float64(n)/dur, 'f', 4, 64)
	}
	scale := "scale='min(1280,iw)':-2"
	if _, err := runIn(ctx, "", "ffmpeg", "-loglevel", "error", "-i", src, "-vf", "fps="+fps+","+scale, "-frames:v", strconv.Itoa(n), filepath.Join(dir, "frame-%03d.png")); err != nil {
		return nil, err
	}
	_, _ = runIn(ctx, "", "ffmpeg", "-loglevel", "error", "-sseof", "-0.5", "-i", src, "-vf", scale, "-update", "1", "-frames:v", "1", filepath.Join(dir, "last.png"))

	entries, _ := os.ReadDir(dir)
	var frames []string
	for _, e := range entries {
		frames = append(frames, filepath.Join("frames", name, e.Name()))
	}
	if len(frames) == 0 {
		return nil, errors.New("ffmpeg wrote no frames; is the video empty?")
	}
	return frames, nil
}

// cmdFrames lets the judge pull more frames from a video than submit took.
func cmdFrames(args []string) error {
	fs := flag.NewFlagSet("frames", flag.ExitOnError)
	runDir := fs.String("run", "", "run directory (default $PRCOURT_RUN)")
	n := fs.Int("n", 30, "how many frames to sample")
	fs.Parse(reorder(args))
	r, err := loadRun(*runDir)
	if err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: prcourt frames [-n N] VIDEO (relative to the run directory)")
	}
	frames, err := extractFrames(r, fs.Arg(0), *n)
	if err != nil {
		return err
	}
	for _, f := range frames {
		fmt.Println(r.path(f))
	}
	return nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
