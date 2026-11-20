package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Check is one dependency doctor looks for.
type Check struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
	OK       bool   `json:"ok"`
	Detail   string `json:"detail,omitempty"`
	Fix      string `json:"fix,omitempty"` // the command or step that installs or repairs it
}

type need struct {
	rex    bool
	agents []string // agent specs to look for, such as claude or codex:gpt-5.5
	role   string   // when set, only what that role's agent runs
}

// doctor checks the tools a run needs. `prcourt run` calls it first, and the
// skill and each agent's instructions run it before starting work.
func doctor(ctx context.Context, n need) []Check {
	var cs []Check
	tool := func(name string, required bool, why, fix string) bool {
		c := Check{Name: name, Required: required, Detail: why, Fix: fix}
		if p, err := exec.LookPath(name); err == nil {
			c.OK, c.Detail, c.Fix = true, p, ""
		}
		cs = append(cs, c)
		return c.OK
	}
	probe := func(name, fix string, args ...string) {
		c := Check{Name: name, Required: true, Fix: fix}
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput()
		c.OK = err == nil
		if !c.OK {
			c.Detail = firstLine(string(out))
		} else {
			c.Fix = ""
		}
		cs = append(cs, c)
	}

	tool("git", true, "checks out the PR in a worktree", install("git"))
	if tool("gh", true, "reads the PR, its diff, comments, and linked issues", install("gh")) {
		probe("gh auth", "gh auth login", "gh", "auth", "status")
	}
	if n.role == "" || n.role == "tester" || n.role == "judge" {
		tool("ffmpeg", true, "extracts frames from e2e videos for the judge", install("ffmpeg"))
		tool("ffprobe", true, "reads video lengths to sample frames evenly", install("ffmpeg"))
	}
	if n.role == "" || n.role == "tester" {
		tool("node", false, "runs Playwright with Chromium for e2e tests when the repository has no e2e framework", install("node"))
		tool("npx", false, "installs and runs Playwright (npx playwright install chromium)", install("node"))
	}
	if n.rex && n.role == "" {
		if tool("rex", true, "opens the TUI and the agents' panes", "install Rex from https://www.superlogical.com/rex, or pass -no-rex") {
			probe("rex server", "open the Rex app so its server runs, or pass -no-rex", "rex", "whoami", "--json")
		}
	}
	seen := map[string]bool{}
	for _, spec := range n.agents {
		name, _, _ := strings.Cut(spec, ":")
		if seen[name] {
			continue
		}
		seen[name] = true
		switch name {
		case "claude":
			tool("claude", true, "runs the agent for roles set to claude", "curl -fsSL https://claude.ai/install.sh | bash, then run claude once to sign in")
		case "codex":
			tool("codex", true, "runs the agent for roles set to codex", "npm install -g @openai/codex, then run codex once to sign in")
		default:
			if f := strings.Fields(spec); len(f) > 0 && !strings.ContainsAny(f[0], "{};|&$") {
				tool(f[0], true, "runs a custom agent command", "install "+f[0])
			}
		}
	}
	return cs
}

func install(pkg string) string {
	if runtime.GOOS == "darwin" {
		return "brew install " + pkg
	}
	return "install " + pkg + " with the system package manager"
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

// missing returns the required checks that failed, as one error.
func missing(cs []Check) error {
	var p problems
	for _, c := range cs {
		if c.Required && !c.OK {
			p.addf("%s is missing or not working (%s); fix: %s", c.Name, c.Detail, c.Fix)
		}
	}
	if err := p.err(); err != nil {
		return fmt.Errorf("dependencies: %w\nRun `prcourt doctor` after fixing them", err)
	}
	return nil
}

func cmdDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	agent := fs.String("agent", "claude", "agent the run will use for every role")
	per := map[string]*string{}
	for _, role := range roles {
		per[role] = fs.String(role, "", "agent for the "+role)
	}
	role := fs.String("role", "", "check only what this role's agent runs: reviewer, tester, or judge")
	noRex := fs.Bool("no-rex", false, "the run will not use Rex")
	asJSON := fs.Bool("json", false, "print JSON")
	fs.Parse(reorder(args))

	n := need{rex: !*noRex, role: *role}
	if *role == "" {
		for _, r := range roles {
			n.agents = append(n.agents, cmpOr(*per[r], *agent))
		}
	}
	cs := doctor(context.Background(), n)
	if *asJSON {
		b, _ := json.MarshalIndent(cs, "", "  ")
		fmt.Println(string(b))
	} else {
		for _, c := range cs {
			mark := "ok  "
			switch {
			case !c.OK && c.Required:
				mark = "FAIL"
			case !c.OK:
				mark = "warn"
			}
			line := fmt.Sprintf("%s %-10s %s", mark, c.Name, c.Detail)
			if c.Fix != "" {
				line += "\n       fix: " + c.Fix
			}
			fmt.Println(line)
		}
	}
	if err := missing(cs); err != nil {
		os.Exit(1)
	}
	return nil
}
