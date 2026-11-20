package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"time"
)

//go:embed prompts/*.md
var promptFS embed.FS

var roles = []string{"reviewer", "tester", "judge"}

// Run is a run directory's run.json: everything later commands and the TUI
// need to find the PR, its checkout, and the Rex panes.
type Run struct {
	Dir      string            `json:"dir"`
	Repo     string            `json:"repo"` // owner/name
	Number   int               `json:"number"`
	URL      string            `json:"url"`
	Title    string            `json:"title"`
	Author   string            `json:"author"`
	Base     string            `json:"base"`
	Head     string            `json:"head"`
	HeadSHA  string            `json:"head_sha"`
	HeadRepo string            `json:"head_repo"` // owner/name of the repository the PR branch lives in
	PushTo   string            `json:"push_to"`   // git remote or URL that pushes to the PR branch
	Worktree string            `json:"worktree"`
	RepoDir  string            `json:"repo_dir,omitempty"` // the local clone the worktree belongs to, if any
	Agents   map[string]string `json:"agents"`             // role -> agent spec, such as claude or codex:gpt-5.5
	Yolo     bool              `json:"yolo"`
	Bin      string            `json:"bin"`
	Created  time.Time         `json:"created"`
	Rex      RexLayout         `json:"rex"`
}

type RexLayout struct {
	Session string            `json:"session,omitempty"`
	Window  string            `json:"window,omitempty"`
	TUI     string            `json:"tui,omitempty"`
	Blocks  map[string]string `json:"blocks,omitempty"` // role -> block id
}

func (r *Run) path(name ...string) string { return filepath.Join(append([]string{r.Dir}, name...)...) }

func loadRun(dir string) (*Run, error) {
	if dir == "" {
		dir = os.Getenv("PRCOURT_RUN")
	}
	if dir == "" {
		return nil, errors.New("no run directory: pass one, or set PRCOURT_RUN")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	var r Run
	if err := readJSON(filepath.Join(abs, "run.json"), &r); err != nil {
		return nil, fmt.Errorf("%s is not a prcourt run: %w", abs, err)
	}
	r.Dir = abs
	return &r, nil
}

func (r *Run) save() error { return writeJSON(r.path("run.json"), r) }

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	repoDir := fs.String("repo-dir", ".", "local clone of the repository; the PR is checked out in a worktree of it")
	repo := fs.String("repo", "", "owner/name; defaults to the repository in -repo-dir")
	agent := fs.String("agent", "claude", "agent for every role: claude, codex, either with :MODEL, or a command with {prompt}")
	per := map[string]*string{}
	for _, role := range roles {
		per[role] = fs.String(role, "", "agent for the "+role+"; defaults to -agent")
	}
	yolo := fs.Bool("yolo", false, "let the agents run every command without asking (bypasses their permission prompts and sandbox)")
	out := fs.String("out", "", "directory for runs (default ~/.prcourt/runs)")
	noRex := fs.Bool("no-rex", false, "prepare the run and print the agent commands instead of opening Rex panes")
	newSession := fs.Bool("new-session", false, "open a new Rex session even inside one")
	asJSON := fs.Bool("json", false, "print run.json")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: prcourt run [flags] PR\n\nPR is a number, a URL, or a branch.")
		fs.PrintDefaults()
	}
	fs.Parse(reorder(args))
	if fs.NArg() != 1 {
		fs.Usage()
		os.Exit(2)
	}
	ctx := context.Background()

	var specs []string
	for _, role := range roles {
		specs = append(specs, cmpOr(*per[role], *agent))
	}
	if err := missing(doctor(ctx, need{rex: !*noRex, agents: specs})); err != nil {
		return err
	}

	repoAbs, _ := filepath.Abs(*repoDir)
	local := ""
	var view struct {
		NameWithOwner string `json:"nameWithOwner"`
	}
	if b, err := runIn(ctx, repoAbs, "gh", "repo", "view", "--json", "nameWithOwner"); err == nil && json.Unmarshal(b, &view) == nil {
		local = view.NameWithOwner
	}
	if *repo == "" {
		if local == "" {
			return fmt.Errorf("%s is not a GitHub repository; pass -repo owner/name", repoAbs)
		}
		*repo = local
	}

	fields := "number,title,body,url,author,baseRefName,headRefName,headRefOid,files,labels,comments,reviews,latestReviews,closingIssuesReferences,statusCheckRollup,isDraft,headRepository"
	prJSON, err := runIn(ctx, repoAbs, "gh", "pr", "view", fs.Arg(0), "--repo", *repo, "--json", fields)
	if err != nil {
		return err
	}
	var pr struct {
		Number   int    `json:"number"`
		Title    string `json:"title"`
		URL      string `json:"url"`
		Base     string `json:"baseRefName"`
		Head     string `json:"headRefName"`
		HeadSHA  string `json:"headRefOid"`
		HeadRepo struct {
			NameWithOwner string `json:"nameWithOwner"`
		} `json:"headRepository"`
		Author struct {
			Login string `json:"login"`
		} `json:"author"`
	}
	if err := json.Unmarshal(prJSON, &pr); err != nil {
		return err
	}

	bin, _ := os.Executable()
	bin, _ = filepath.EvalSymlinks(bin)
	r := &Run{
		Repo: *repo, Number: pr.Number, URL: pr.URL, Title: pr.Title, Author: pr.Author.Login,
		Base: pr.Base, Head: pr.Head, HeadSHA: pr.HeadSHA, HeadRepo: cmpOr(pr.HeadRepo.NameWithOwner, *repo),
		Agents: map[string]string{}, Yolo: *yolo, Bin: bin, Created: time.Now(),
	}
	for _, role := range roles {
		r.Agents[role] = cmpOr(*per[role], *agent)
		if _, err := agentArgv(r, role); err != nil {
			return err
		}
	}
	base := *out
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".prcourt", "runs")
	}
	r.Dir = filepath.Join(base, strings.ReplaceAll(*repo, "/", "__"), fmt.Sprintf("pr-%d-%s", pr.Number, r.Created.Format("20060102-150405")))
	r.Worktree = r.path("worktree")
	for _, d := range []string{"prompts", "drafts", "artifacts", "frames"} {
		if err := os.MkdirAll(r.path(d), 0o755); err != nil {
			return err
		}
	}
	if err := os.WriteFile(r.path("pr.json"), prJSON, 0o644); err != nil {
		return err
	}

	diff, err := runIn(ctx, repoAbs, "gh", "pr", "diff", strconv.Itoa(pr.Number), "--repo", *repo)
	if err != nil {
		return err
	}
	if err := os.WriteFile(r.path("pr.diff"), diff, 0o644); err != nil {
		return err
	}
	// Inline review comments are not in `gh pr view`; flatten the pages.
	if b, err := runIn(ctx, repoAbs, "gh", "api", fmt.Sprintf("repos/%s/pulls/%d/comments", *repo, pr.Number), "--paginate", "--slurp"); err == nil {
		var pages [][]json.RawMessage
		if json.Unmarshal(b, &pages) == nil {
			var all []json.RawMessage
			for _, p := range pages {
				all = append(all, p...)
			}
			b, _ = json.MarshalIndent(all, "", "  ")
		}
		_ = os.WriteFile(r.path("review-comments.json"), b, 0o644)
	}

	if err := checkout(ctx, r, repoAbs, local == *repo); err != nil {
		return fmt.Errorf("checking out the PR: %w", err)
	}
	if err := writePrompts(r); err != nil {
		return err
	}
	if err := r.save(); err != nil {
		return err
	}
	logEvent(r, "run created for %s#%d at %s", r.Repo, r.Number, short(r.HeadSHA))

	if *noRex {
		fmt.Printf("Run: %s\n\n", r.Dir)
		for _, role := range roles[:2] {
			argv, _ := agentArgv(r, role)
			fmt.Printf("%s (in %s):\n  %s\n\n", role, r.Worktree, shellJoin(argv))
		}
		fmt.Println("The judge starts after both submit; with -no-rex start it with: prcourt judge " + r.Dir)
		return nil
	}
	if err := openLayout(ctx, r, *newSession); err != nil {
		return err
	}
	if *asJSON {
		b, _ := json.MarshalIndent(r, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	fmt.Printf("Started review of %s#%d: %s\nRun: %s\nRex session %s, window %s\n", r.Repo, r.Number, r.Title, r.Dir, r.Rex.Session, r.Rex.Window)
	return nil
}

// checkout puts the PR head in r.Worktree: a worktree of the local clone when
// it is the same repository, so nothing is downloaded twice and the user's
// own checkout stays as it is, or else a fresh clone.
func checkout(ctx context.Context, r *Run, repoDir string, sameRepo bool) error {
	if sameRepo {
		if _, err := runIn(ctx, repoDir, "git", "worktree", "add", "--detach", r.Worktree, "HEAD"); err != nil {
			return err
		}
		if top, err := runIn(ctx, repoDir, "git", "rev-parse", "--show-toplevel"); err == nil {
			r.RepoDir = strings.TrimSpace(string(top))
		}
	} else if _, err := runIn(ctx, r.Dir, "gh", "repo", "clone", r.Repo, r.Worktree, "--", "--filter=blob:none"); err != nil {
		return err
	}
	if _, err := runIn(ctx, r.Worktree, "gh", "pr", "checkout", strconv.Itoa(r.Number), "--repo", r.Repo, "--detach"); err != nil {
		return err
	}
	// Push the way the clone already talks to GitHub, so SSH users keep SSH.
	r.PushTo = "origin"
	if r.HeadRepo != r.Repo {
		r.PushTo = "https://github.com/" + r.HeadRepo + ".git"
		if url, err := runIn(ctx, r.Worktree, "git", "remote", "get-url", "origin"); err == nil && !strings.HasPrefix(string(url), "https://") {
			r.PushTo = "git@github.com:" + r.HeadRepo + ".git"
		}
	}
	return nil
}

func writePrompts(r *Run) error {
	for _, role := range roles {
		src, err := promptFS.ReadFile("prompts/" + role + ".md")
		if err != nil {
			return err
		}
		t, err := template.New(role).Parse(string(src))
		if err != nil {
			return err
		}
		var b bytes.Buffer
		if err := t.Execute(&b, r); err != nil {
			return err
		}
		if err := os.WriteFile(r.path("prompts", role+".md"), b.Bytes(), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// agentArgv builds the command that starts role's agent. The agent reads its
// instructions from the run's prompt file, so the command line stays short.
func agentArgv(r *Run, role string) ([]string, error) {
	prompt := fmt.Sprintf("You are the %s in a prcourt pull request review of %s#%d. Read %s and follow it.", role, r.Repo, r.Number, r.path("prompts", role+".md"))
	env := []string{"env", "PRCOURT_RUN=" + r.Dir, "PRCOURT_ROLE=" + role, "PATH=" + filepath.Dir(r.Bin) + ":" + os.Getenv("PATH")}
	spec := r.Agents[role]
	name, model, _ := strings.Cut(spec, ":")
	switch name {
	case "claude":
		argv := []string{"claude", "--add-dir", r.Dir}
		if r.Yolo {
			argv = append(argv, "--dangerously-skip-permissions")
		} else {
			argv = append(argv, "--permission-mode", "acceptEdits")
		}
		if model != "" {
			argv = append(argv, "--model", model)
		}
		return append(env, append(argv, prompt)...), nil
	case "codex":
		argv := []string{"codex", "--cd", r.Worktree, "--add-dir", r.Dir}
		if r.Yolo {
			argv = append(argv, "--dangerously-bypass-approvals-and-sandbox")
		} else {
			argv = append(argv, "--sandbox", "workspace-write", "--ask-for-approval", "on-request")
		}
		if model != "" {
			argv = append(argv, "--model", model)
		}
		return append(env, append(argv, prompt)...), nil
	}
	if strings.Contains(spec, "{prompt}") {
		cmd := strings.NewReplacer("{prompt}", shellQuote(prompt), "{run}", shellQuote(r.Dir), "{worktree}", shellQuote(r.Worktree)).Replace(spec)
		return append(env, "sh", "-c", cmd), nil
	}
	return nil, fmt.Errorf("agent %q: use claude, codex, NAME:MODEL, or a command containing {prompt}", spec)
}

// openLayout opens the run's Rex tab: the TUI on the left, the reviewer and
// the tester stacked on the right. The judge joins later, beside the reviewer.
func openLayout(ctx context.Context, r *Run, newSession bool) error {
	tui := []string{r.Bin, "tui", r.Dir}
	name := fmt.Sprintf("PR #%d", r.Number)
	if s := os.Getenv("REX_SESSION"); s != "" && !newSession {
		var w struct {
			WindowID string   `json:"window_id"`
			BlockIDs []string `json:"block_ids"`
		}
		if err := rexJSON(ctx, &w, append([]string{"window", "new", "-s", s, name, "-c", r.Dir, "--keep-open", "--json", "--"}, tui...)...); err != nil {
			return err
		}
		r.Rex = RexLayout{Session: s, Window: w.WindowID, TUI: first(w.BlockIDs)}
	} else {
		var n struct {
			SessionID string `json:"session_id"`
			Windows   []struct {
				WindowID string   `json:"window_id"`
				BlockIDs []string `json:"block_ids"`
			} `json:"initial_windows"`
		}
		label := fmt.Sprintf("pr-%d-review", r.Number)
		if err := rexJSON(ctx, &n, append([]string{"new", label, "-c", r.Dir, "--window", name, "--keep-open", "--json", "--"}, tui...)...); err != nil {
			return err
		}
		if len(n.Windows) == 0 {
			return errors.New("rex new returned no window")
		}
		r.Rex = RexLayout{Session: n.SessionID, Window: n.Windows[0].WindowID, TUI: first(n.Windows[0].BlockIDs)}
	}
	r.Rex.Blocks = map[string]string{}
	if err := r.save(); err != nil {
		return err
	}
	if err := startAgent(ctx, r, "reviewer", r.Rex.TUI, "right", 65); err != nil {
		return err
	}
	return startAgent(ctx, r, "tester", r.Rex.Blocks["reviewer"], "below", 50)
}

func startAgent(ctx context.Context, r *Run, role, anchor, dir string, ratio int) error {
	argv, err := agentArgv(r, role)
	if err != nil {
		return err
	}
	args := []string{"split", "-s", r.Rex.Session, "--split", dir, "--ratio", strconv.Itoa(ratio),
		"--label", titleCase(role), "--focus=false", "--keep-open", "-c", r.Worktree, "--json"}
	if anchor != "" {
		args = append(args, "-b", anchor)
	} else {
		args = append(args, "-w", r.Rex.Window)
	}
	var s struct {
		BlockIDs []string `json:"block_ids"`
	}
	if err := rexJSON(ctx, &s, append(append(args, "--"), argv...)...); err != nil {
		// The anchor pane may be gone, such as a closed TUI; open a tab instead.
		var w struct {
			BlockIDs []string `json:"block_ids"`
		}
		if err2 := rexJSON(ctx, &w, append([]string{"window", "new", "-s", r.Rex.Session, titleCase(role), "-c", r.Worktree, "--keep-open", "--focus=false", "--json", "--"}, argv...)...); err2 != nil {
			return fmt.Errorf("starting the %s: %w", role, err)
		}
		s.BlockIDs = w.BlockIDs
	}
	r.Rex.Blocks[role] = first(s.BlockIDs)
	logEvent(r, "%s started (%s)", role, r.Agents[role])
	return r.save()
}

func rexJSON(ctx context.Context, v any, args ...string) error {
	b, err := runIn(ctx, "", "rex", args...)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func runIn(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if ee, ok := err.(*exec.ExitError); ok {
		return out, fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), strings.TrimSpace(string(ee.Stderr)))
	}
	return out, err
}

func logEvent(r *Run, format string, a ...any) {
	f, err := os.OpenFile(r.path("events.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().Format(time.TimeOnly), fmt.Sprintf(format, a...))
}

// reorder moves flags ahead of positional arguments, so `prcourt run 12 -yolo` works.
func reorder(args []string) []string {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			if !strings.Contains(a, "=") && i+1 < len(args) && !isBoolFlag(a) {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		pos = append(pos, a)
	}
	return append(flags, pos...)
}

func isBoolFlag(a string) bool {
	switch strings.TrimLeft(a, "-") {
	case "yolo", "no-rex", "new-session", "json", "force", "h", "help":
		return true
	}
	return false
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func shellJoin(argv []string) string {
	q := make([]string, len(argv))
	for i, a := range argv {
		q[i] = shellQuote(a)
	}
	return strings.Join(q, " ")
}

func first(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func short(sha string) string { return sha[:min(7, len(sha))] }

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
