package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type paneState struct{ State, Kind, Msg string }

type snapshot struct {
	run     *Run
	review  *Review
	tests   *TestReport
	verdict *Verdict
	panes   map[string]paneState
	events  []string
	err     error
}

type (
	snapMsg snapshot
	tickMsg struct{}
	noteMsg string
)

var tabs = []string{"Findings", "Tests", "Requirements", "Verdict", "Log"}

type model struct {
	dir           string
	s             snapshot
	tab           int
	cursor        [5]int
	open          bool // show the selected row's details
	note          string
	width, height int
}

func cmdTUI(args []string) error {
	r, err := loadRun(first(args))
	if err != nil {
		return err
	}
	m := model{dir: r.Dir, s: load(r.Dir)}
	_, err = tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

// load reads everything the TUI shows. Files that are not there yet stay nil.
func load(dir string) snapshot {
	r, err := loadRun(dir)
	if err != nil {
		return snapshot{err: err}
	}
	s := snapshot{run: r, panes: map[string]paneState{}}
	var rv Review
	if readJSON(r.path("review.json"), &rv) == nil {
		s.review = &rv
	}
	var tr TestReport
	if readJSON(r.path("tests.json"), &tr) == nil {
		s.tests = &tr
	}
	var v Verdict
	if readJSON(r.path("verdict.json"), &v) == nil {
		s.verdict = &v
	}
	if b, err := os.ReadFile(r.path("events.log")); err == nil {
		s.events = strings.Split(strings.TrimSpace(string(b)), "\n")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for role, block := range r.Rex.Blocks {
		var st struct {
			Records []struct{ State, Kind, Msg string } `json:"records"`
		}
		if err := rexJSON(ctx, &st, "block", "call", "-s", r.Rex.Session, "-b", block, "program_status"); err != nil {
			s.panes[role] = paneState{State: "gone"}
			continue
		}
		var best paneState
		for _, rec := range st.Records {
			if stateRank(rec.State) > stateRank(best.State) {
				best = paneState(rec)
			}
		}
		// Agents don't always report a Rex status while a permission or trust
		// prompt is open, so look for one on screen.
		if best.State != "blocked" && !exists(r.path(outputs[role])) {
			if out, err := runIn(ctx, "", "rex", "capture", "-s", r.Rex.Session, "-b", block, "--trim"); err == nil && onScreenPrompt(string(out)) {
				best = paneState{State: "blocked", Kind: "prompt"}
			}
		}
		s.panes[role] = best
	}
	return s
}

// onScreenPrompt reports whether the bottom of a pane shows a menu waiting
// for an answer, such as Claude Code's or Codex's approval prompts.
func onScreenPrompt(screen string) bool {
	lines := strings.Split(strings.TrimRight(screen, " \n"), "\n")
	tail := strings.ToLower(strings.Join(lines[max(0, len(lines)-6):], "\n"))
	for _, p := range []string{"enter to confirm", "esc to cancel", "do you want to proceed", "press enter to"} {
		if strings.Contains(tail, p) {
			return true
		}
	}
	return false
}

// stateRank orders Rex program states so a blocked child beats a working root.
func stateRank(s string) int {
	return map[string]int{"idle": 1, "working": 2, "done": 3, "error": 4, "blocked": 5}[s]
}

func (m model) Init() tea.Cmd { return tick() }

func tick() tea.Cmd { return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return tickMsg{} }) }

func (m model) refresh() tea.Cmd { return func() tea.Msg { return snapMsg(load(m.dir)) } }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tickMsg:
		return m, tea.Batch(m.refresh(), tick())
	case snapMsg:
		m.s = snapshot(msg)
	case noteMsg:
		m.note = string(msg)
	case tea.KeyMsg:
		m.note = ""
		switch k := msg.String(); k {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "tab", "right", "l":
			m.tab, m.open = (m.tab+1)%len(tabs), false
		case "shift+tab", "left", "h":
			m.tab, m.open = (m.tab+len(tabs)-1)%len(tabs), false
		case "1", "2", "3", "4", "5":
			m.tab, m.open = int(k[0]-'1'), false
		case "down", "j":
			m.cursor[m.tab] = min(m.cursor[m.tab]+1, max(0, m.rows()-1))
		case "up", "k":
			m.cursor[m.tab] = max(m.cursor[m.tab]-1, 0)
		case "enter", " ":
			m.open = !m.open
		case "r", "t", "g":
			return m, m.focus(map[string]string{"r": "reviewer", "t": "tester", "g": "judge"}[k])
		case "o":
			return m, m.openSelected()
		case "a":
			if m.s.run != nil {
				return m, openPath(m.s.run.path("artifacts"))
			}
		case "J":
			return m, m.startJudge()
		}
	}
	return m, nil
}

func (m model) rows() int {
	switch m.tab {
	case 0:
		if m.s.review != nil {
			return len(m.s.review.Findings)
		}
	case 1:
		if m.s.tests != nil {
			return len(m.s.tests.Tests)
		}
	case 2:
		if m.s.review != nil {
			return len(m.s.review.Requirements)
		}
	}
	return 0
}

func (m model) focus(role string) tea.Cmd {
	r := m.s.run
	return func() tea.Msg {
		if r == nil || r.Rex.Blocks[role] == "" {
			return noteMsg("the " + role + " has not started")
		}
		if _, err := runIn(context.Background(), "", "rex", "focus", "-s", r.Rex.Session, r.Rex.Blocks[role]); err != nil {
			return noteMsg(err.Error())
		}
		return noteMsg("focused the " + role)
	}
}

// openSelected opens the selected test's video or screenshot, or the
// selected finding's file at its line in $EDITOR in a Rex pane.
func (m model) openSelected() tea.Cmd {
	r := m.s.run
	switch {
	case m.tab == 1 && m.s.tests != nil && m.cursor[1] < len(m.s.tests.Tests):
		t := m.s.tests.Tests[m.cursor[1]]
		for _, kind := range []string{"video", "screenshot", "trace", "log"} {
			for _, a := range m.s.tests.Artifacts {
				if a.TestID == t.ID && a.Type == kind {
					return openPath(r.path(a.Path))
				}
			}
		}
		return func() tea.Msg { return noteMsg("this test has no artifacts") }
	case m.tab == 0 && m.s.review != nil && m.cursor[0] < len(m.s.review.Findings):
		f := m.s.review.Findings[m.cursor[0]]
		if f.File == "" {
			return nil
		}
		editor := cmpOr(os.Getenv("EDITOR"), "vi")
		return func() tea.Msg {
			_, err := runIn(context.Background(), "", "rex", "split", "-s", r.Rex.Session, "-b", r.Rex.TUI, "--split", "below",
				"-c", r.Worktree, "--label", f.File, "--", editor, fmt.Sprintf("+%d", max(f.Line, 1)), f.File)
			if err != nil {
				return noteMsg(err.Error())
			}
			return nil
		}
	}
	return nil
}

func (m model) startJudge() tea.Cmd {
	r := m.s.run
	return func() tea.Msg {
		if r == nil || exists(r.path("judge.started")) {
			return noteMsg("the judge has already started")
		}
		if !exists(r.path("review.json")) || !exists(r.path("tests.json")) {
			// The judge needs both files; write empty stand-ins it will see as missing work.
			return noteMsg("the judge needs review.json and tests.json; ask the agent that has not submitted to finish")
		}
		if err := maybeStartJudge(r); err != nil {
			return noteMsg(err.Error())
		}
		return noteMsg("started the judge")
	}
}

func openPath(p string) tea.Cmd {
	return func() tea.Msg {
		if err := exec.Command("open", p).Start(); err != nil {
			return noteMsg(err.Error())
		}
		return noteMsg("opened " + filepath.Base(p))
	}
}

// Styles.
var (
	dim    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	bold   = lipgloss.NewStyle().Bold(true)
	green  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	red    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	yellow = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	blue   = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
	sel    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("4"))
	card   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("8")).Padding(0, 1)
)

func (m model) View() string {
	if m.s.err != nil {
		return red.Render(m.s.err.Error())
	}
	r := m.s.run
	w := max(m.width, 40)
	var b strings.Builder
	b.WriteString(bold.Render(fmt.Sprintf("%s#%d", r.Repo, r.Number)) + " " + trunc(r.Title, w-len(r.Repo)-8) + "\n")
	b.WriteString(dim.Render(fmt.Sprintf("%s ← %s · %s · @%s", r.Base, r.Head, short(r.HeadSHA), r.Author)) + "\n")

	cardW := max((w-2)/3-2, 12)
	var cards []string
	for _, role := range roles {
		cards = append(cards, card.Width(cardW).Render(bold.Render(titleCase(role))+"\n"+m.stage(role)+"\n"+dim.Render(trunc(agentName(r.Agents[role]), cardW-2))))
	}
	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, cards...) + "\n")

	if v := m.s.verdict; v != nil {
		b.WriteString(decisionStyle(v.Decision).Render(" "+strings.ToUpper(strings.ReplaceAll(v.Decision, "_", " "))+" ") + " " + trunc(oneLine(v.Summary), w-20) + "\n")
	}

	var tabLine []string
	for i, t := range tabs {
		label := fmt.Sprintf(" %d %s ", i+1, t)
		if i == m.tab {
			label = sel.Render(label)
		}
		tabLine = append(tabLine, label)
	}
	b.WriteString(strings.Join(tabLine, "") + "\n\n")

	used := strings.Count(b.String(), "\n")
	body := m.body(w, max(m.height-used-2, 3))
	b.WriteString(body)

	footer := "tab switch · j/k move · enter details · o open · a artifacts · r/t/g focus agent · J judge now · q quit"
	if m.note != "" {
		footer = m.note
	}
	pad := m.height - strings.Count(b.String(), "\n") - 1
	if pad > 0 {
		b.WriteString(strings.Repeat("\n", pad))
	}
	b.WriteString(dim.Render(trunc(footer, w)))
	return b.String()
}

// stage says where a role stands, combining its output file with what Rex
// reports for its pane.
func (m model) stage(role string) string {
	r := m.s.run
	if exists(r.path(outputs[role])) {
		return green.Render("✓ submitted")
	}
	if r.Rex.Blocks[role] == "" {
		if role == "judge" {
			return dim.Render("waits for both")
		}
		return dim.Render("not started")
	}
	p := m.s.panes[role]
	switch p.State {
	case "blocked":
		return red.Render("● needs you: " + cmpOr(p.Kind, "input"))
	case "error":
		return red.Render("● error " + p.Msg)
	case "done", "idle":
		return yellow.Render("● stopped, not submitted")
	case "gone":
		return red.Render("● pane closed")
	case "working":
		return blue.Render("● working")
	}
	return blue.Render("● running")
}

// agentName shortens a custom agent command for the cards.
func agentName(spec string) string {
	if strings.Contains(spec, "{prompt}") {
		return "custom command"
	}
	return spec
}

func decisionStyle(d string) lipgloss.Style {
	s := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0"))
	switch d {
	case "approve":
		return s.Background(lipgloss.Color("2"))
	case "request_changes":
		return s.Background(lipgloss.Color("1"))
	}
	return s.Background(lipgloss.Color("3"))
}

func (m model) body(w, h int) string {
	var lines []string
	var detail string
	cur := m.cursor[m.tab]
	switch m.tab {
	case 0:
		rv := m.s.review
		if rv == nil {
			return dim.Render("The reviewer has not submitted yet.")
		}
		rulings := m.rulings()
		for i, f := range rv.Findings {
			loc := ""
			if f.File != "" {
				loc = fmt.Sprintf("%s:%d", f.File, f.Line)
			}
			flag := ""
			if f.Anchor != nil && f.Anchor.Status != "in_diff" {
				flag = yellow.Render(" ⚑" + f.Anchor.Status)
			}
			ruling := ""
			if x, ok := rulings[f.ID]; ok {
				ruling = " " + rulingStyle(x.Ruling).Render(x.Ruling)
			}
			lines = append(lines, row(i == cur, fmt.Sprintf("%s %-4s %s  %s", sevTag(f.Severity), f.ID, trunc(f.Title, w/2), dim.Render(trunc(loc, w/3)))+flag+ruling))
			if i == cur && m.open {
				detail = wrap(f.Body, w) + "\n\n" + wrap(dim.Render("Evidence: ")+f.Evidence, w)
				if f.Quote != "" {
					detail += "\n\n" + dim.Render(trunc(f.Quote, w*3))
				}
				if f.Suggestion != "" {
					detail += "\n\n" + green.Render("Suggestion:\n") + f.Suggestion
				}
				if f.Anchor != nil && f.Anchor.Note != "" {
					detail += "\n\n" + yellow.Render(wrap("prcourt: "+f.Anchor.Note, w))
				}
				if x, ok := rulings[f.ID]; ok {
					detail += "\n\n" + wrap(bold.Render("Judge ("+x.Ruling+"): ")+x.Reason, w)
				}
			}
		}
	case 1:
		tr := m.s.tests
		if tr == nil {
			return dim.Render("The tester has not submitted yet.")
		}
		trust := map[string]TestAssessment{}
		if m.s.verdict != nil {
			for _, a := range m.s.verdict.Tests {
				trust[a.TestID] = a
			}
		}
		for i, t := range tr.Tests {
			n := 0
			for _, a := range tr.Artifacts {
				if a.TestID == t.ID {
					n++
				}
			}
			flag := ""
			if a, ok := trust[t.ID]; ok && !a.Trusted {
				flag = red.Render(" ⚠ untrusted")
			}
			lines = append(lines, row(i == cur, fmt.Sprintf("%s %-12s %-4s %s  %s", statusMark(t.Status), t.Kind, t.ID, trunc(t.Name, w/2), dim.Render(fmt.Sprintf("%d artifacts", n)))+flag))
			if i == cur && m.open {
				detail = dim.Render("$ ") + t.Command
				if t.Notes != "" {
					detail += "\n\n" + wrap(t.Notes, w)
				}
				for _, a := range tr.Artifacts {
					if a.TestID == t.ID {
						detail += fmt.Sprintf("\n%-10s %s %s", a.Type, a.Path, dim.Render(a.Caption))
						if len(a.Frames) > 0 {
							detail += dim.Render(fmt.Sprintf(" (%d frames)", len(a.Frames)))
						}
					}
				}
				if a, ok := trust[t.ID]; ok {
					detail += "\n\n" + wrap(bold.Render("Judge: ")+a.Reason, w)
				}
			}
		}
		if cur == 0 && !m.open {
			lines = append([]string{dim.Render(wrap("Conventions: "+tr.Conventions, w)), ""}, lines...)
		}
	case 2:
		rv := m.s.review
		if rv == nil {
			return dim.Render("The reviewer lists the requirements when it submits.")
		}
		checks := map[string]RequirementCheck{}
		if m.s.verdict != nil {
			for _, c := range m.s.verdict.Requirements {
				checks[c.ID] = c
			}
		}
		for i, q := range rv.Requirements {
			mark := dim.Render("·")
			c, ok := checks[q.ID]
			if ok {
				mark = map[string]string{"met": green.Render("✓"), "unmet": red.Render("✗"), "unverified": yellow.Render("?")}[c.Status]
			}
			lines = append(lines, row(i == cur, fmt.Sprintf("%s %-4s %s  %s", mark, q.ID, trunc(q.Text, w-20), dim.Render(q.Source))))
			if i == cur && m.open && ok {
				detail = wrap(c.Reason, w) + "\n" + dim.Render(wrap("Evidence: "+strings.Join(c.Evidence, ", "), w))
			}
		}
	case 3:
		v := m.s.verdict
		if v == nil {
			return dim.Render("The judge has not decided yet.")
		}
		out := decisionStyle(v.Decision).Render(" "+v.Decision+" ") + "\n\n" + wrap(v.Summary, w)
		for _, x := range v.Missed {
			out += "\n\n" + sevTag(x.Severity) + " Missed by the reviewer: " + x.Title + dim.Render(" "+where(x)) + "\n" + wrap(x.Body, w)
		}
		if exists(m.s.run.path("verdict.md")) {
			out += "\n\n" + dim.Render(wrap("Report: "+m.s.run.path("verdict.md"), w))
		}
		return clip(out, h)
	case 4:
		return clip(strings.Join(m.s.events[max(0, len(m.s.events)-h):], "\n"), h)
	}
	if len(lines) == 0 {
		return dim.Render("Nothing here.")
	}
	if detail != "" {
		h = max(h/2, 3)
	}
	start := max(0, min(cur-h/2, len(lines)-h))
	out := strings.Join(lines[start:min(len(lines), start+h)], "\n")
	if detail != "" {
		out += "\n" + dim.Render(strings.Repeat("─", w)) + "\n" + detail
	}
	return out
}

func (m model) rulings() map[string]Ruling {
	out := map[string]Ruling{}
	if m.s.verdict != nil {
		for _, x := range m.s.verdict.Rulings {
			out[x.FindingID] = x
		}
	}
	return out
}

func sevTag(s string) string {
	switch s {
	case "blocker":
		return red.Bold(true).Render("BLK")
	case "major":
		return red.Render("MAJ")
	case "minor":
		return yellow.Render("min")
	}
	return dim.Render("nit")
}

func rulingStyle(r string) lipgloss.Style {
	switch {
	case r == "upheld":
		return red
	case r == "downgraded":
		return yellow
	}
	return dim.Strikethrough(true)
}

func row(selected bool, s string) string {
	if selected {
		return "▸ " + s
	}
	return "  " + s
}

func trunc(s string, n int) string {
	s = oneLine(s)
	if n <= 1 || len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

func wrap(s string, w int) string { return lipgloss.NewStyle().Width(max(w-2, 10)).Render(s) }

func clip(s string, h int) string {
	lines := strings.Split(s, "\n")
	return strings.Join(lines[:min(len(lines), h)], "\n")
}
