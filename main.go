// prcourt reviews a pull request with three coding agents in Rex panes: a
// reviewer that reads the PR and suggests changes, a tester that runs and
// writes tests and records the e2e runs, and a judge that weighs both and
// decides. A TUI beside them shows where the review stands.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"
)

const usage = `prcourt: review a pull request with a reviewer, a tester, and a judge.

Commands:
  doctor        check required tools and optional Rex and terminal-browser
  run PR        check out PR; open Rex panes if available, otherwise print commands
  tui [RUN]     show a run's progress, findings, tests, and verdict
  status [RUN]  print where a run stands (-json for machines)
  wait [RUN]    wait until the judge decides (-timeout 10m)
  submit ROLE   validate and hand in an agent's draft (agents run this)
  frames VIDEO  sample frames from a test video (the judge runs this)
  judge [RUN]   start the judge now, even if an agent has not submitted
  report [RUN]  print the verdict as Markdown
  post [RUN]    post the report to the PR as a review comment
  clean [RUN]   remove the run's checkout

RUN is a run directory and defaults to $PRCOURT_RUN.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmds := map[string]func([]string) error{
		"doctor": cmdDoctor, "run": cmdRun, "tui": cmdTUI, "status": cmdStatus, "wait": cmdWait, "submit": cmdSubmit,
		"frames": cmdFrames, "judge": cmdJudge, "report": cmdReport, "post": cmdPost, "clean": cmdClean,
	}
	cmd, ok := cmds[os.Args[1]]
	if !ok {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err := cmd(os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "prcourt:", err)
		os.Exit(1)
	}
}

// Status is where a run stands, read from its files.
type Status struct {
	Run      string            `json:"run"`
	PR       string            `json:"pr"`
	Stages   map[string]string `json:"stages"` // role -> waiting, running, submitted
	Decision string            `json:"decision,omitempty"`
	Report   string            `json:"report,omitempty"`
}

func statusOf(r *Run) Status {
	s := Status{Run: r.Dir, PR: fmt.Sprintf("%s#%d", r.Repo, r.Number), Stages: map[string]string{}}
	for _, role := range roles {
		switch {
		case exists(r.path(outputs[role])):
			s.Stages[role] = "submitted"
		case r.Rex.Blocks[role] != "":
			s.Stages[role] = "running"
		default:
			s.Stages[role] = "waiting"
		}
	}
	var v Verdict
	if readJSON(r.path("verdict.json"), &v) == nil {
		s.Decision, s.Report = v.Decision, r.path("verdict.md")
	}
	return s
}

func cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "print JSON")
	fs.Parse(reorder(args))
	r, err := loadRun(fs.Arg(0))
	if err != nil {
		return err
	}
	s := statusOf(r)
	if *asJSON {
		b, _ := json.MarshalIndent(s, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	fmt.Printf("%s %s\n", s.PR, r.Title)
	for _, role := range roles {
		fmt.Printf("  %-8s %s\n", role, s.Stages[role])
	}
	if s.Decision != "" {
		fmt.Printf("Decision: %s (%s)\n", s.Decision, s.Report)
	}
	return nil
}

func cmdWait(args []string) error {
	fs := flag.NewFlagSet("wait", flag.ExitOnError)
	timeout := fs.Duration("timeout", 10*time.Minute, "give up after this long; the run keeps going")
	fs.Parse(reorder(args))
	r, err := loadRun(fs.Arg(0))
	if err != nil {
		return err
	}
	deadline := time.Now().Add(*timeout)
	last := ""
	for {
		s := statusOf(r)
		line := fmt.Sprintf("reviewer %s, tester %s, judge %s", s.Stages["reviewer"], s.Stages["tester"], s.Stages["judge"])
		if line != last {
			fmt.Println(time.Now().Format(time.TimeOnly), line)
			last = line
		}
		if s.Decision != "" {
			fmt.Printf("Decision: %s\nReport: %s\n", s.Decision, s.Report)
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("timed out waiting for the verdict; the agents are still working, so wait again")
		}
		time.Sleep(5 * time.Second)
		if r2, err := loadRun(r.Dir); err == nil {
			r = r2 // the judge's pane is added to run.json when it starts
		}
	}
}

func cmdReport(args []string) error {
	r, err := loadRun(first(args))
	if err != nil {
		return err
	}
	b, err := os.ReadFile(r.path("verdict.md"))
	if err != nil {
		return errors.New("there is no verdict yet")
	}
	fmt.Print(string(b))
	return nil
}

// cmdPost posts the report as a comment review. It never approves or requests
// changes on GitHub; people decide that.
func cmdPost(args []string) error {
	r, err := loadRun(first(args))
	if err != nil {
		return err
	}
	if !exists(r.path("verdict.md")) {
		return errors.New("there is no verdict yet")
	}
	_, err = runIn(context.Background(), r.Worktree, "gh", "pr", "review", strconv.Itoa(r.Number), "--repo", r.Repo, "--comment", "--body-file", r.path("verdict.md"))
	if err == nil {
		logEvent(r, "posted the report to %s", r.URL)
		fmt.Println("Posted the report to", r.URL)
	}
	return err
}

func cmdClean(args []string) error {
	r, err := loadRun(first(args))
	if err != nil {
		return err
	}
	ctx := context.Background()
	if r.RepoDir != "" {
		if _, err := runIn(ctx, r.RepoDir, "git", "worktree", "remove", "--force", r.Worktree); err != nil {
			return err
		}
	} else if err := os.RemoveAll(r.Worktree); err != nil {
		return err
	}
	fmt.Println("Removed", r.Worktree, "; kept the reports and artifacts in", r.Dir)
	return nil
}
