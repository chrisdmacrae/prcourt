package main

import (
	"fmt"
	"strings"
)

// renderReport writes the verdict as Markdown for people: the decision, what
// the PR had to do, the findings that stand, the ones the judge threw out and
// why, and the test evidence to look at.
func renderReport(r *Run, rv *Review, tr *TestReport, v *Verdict) string {
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }

	label := map[string]string{"approve": "✅ Approve", "request_changes": "❌ Request changes", "needs_human": "🟡 Needs a human"}[v.Decision]
	w("## prcourt: %s\n\n", label)
	w("%s\n\n", strings.TrimSpace(v.Summary))
	w("Reviewed %s at `%s`; reviewer %s, tester %s, judge %s.\n\n", r.URL, short(r.HeadSHA), r.Agents["reviewer"], r.Agents["tester"], r.Agents["judge"])

	reqs := map[string]Requirement{}
	for _, q := range rv.Requirements {
		reqs[q.ID] = q
	}
	w("### Requirements\n\n")
	for _, c := range v.Requirements {
		mark := map[string]string{"met": "✅", "unmet": "❌", "unverified": "❔"}[c.Status]
		w("- %s **%s** %s: %s", mark, c.ID, reqs[c.ID].Text, c.Reason)
		if len(c.Evidence) > 0 {
			w(" (evidence: %s)", strings.Join(c.Evidence, ", "))
		}
		w("\n")
	}

	findings := map[string]Finding{}
	for _, f := range rv.Findings {
		findings[f.ID] = f
	}
	var kept, dropped []Ruling
	for _, x := range v.Rulings {
		if strings.HasPrefix(x.Ruling, "dismissed") {
			dropped = append(dropped, x)
		} else {
			kept = append(kept, x)
		}
	}
	if len(kept)+len(v.Missed) > 0 {
		w("\n### Findings that stand\n\n")
		for _, x := range kept {
			f := findings[x.FindingID]
			w("- **%s** [%s] %s%s\n  %s\n", f.ID, x.Severity, f.Title, where(f), oneLine(f.Body))
			if x.Ruling == "downgraded" {
				w("  _Downgraded from %s: %s_\n", f.Severity, oneLine(x.Reason))
			}
		}
		for _, m := range v.Missed {
			w("- **%s** [%s] %s%s (found by the judge)\n  %s\n", m.ID, m.Severity, m.Title, where(m), oneLine(m.Body))
		}
	}
	if len(dropped) > 0 {
		w("\n<details><summary>%d finding(s) dismissed by the judge</summary>\n\n", len(dropped))
		for _, x := range dropped {
			f := findings[x.FindingID]
			w("- **%s** %s (%s): %s\n", f.ID, f.Title, strings.TrimPrefix(x.Ruling, "dismissed_"), oneLine(x.Reason))
		}
		w("\n</details>\n")
	}

	trusted := map[string]TestAssessment{}
	for _, a := range v.Tests {
		trusted[a.TestID] = a
	}
	w("\n### Tests\n\n")
	for _, t := range tr.Tests {
		note := ""
		if a := trusted[t.ID]; !a.Trusted {
			note = " ⚠️ not trusted by the judge: " + oneLine(a.Reason)
		}
		w("- %s **%s** %s `%s`: %s%s\n", statusMark(t.Status), t.Kind, t.Name, t.Command, t.Status, note)
	}
	w("\nArtifacts (videos, screenshots, logs) and extracted frames are in `%s`.\n", r.Dir)
	return b.String()
}

func where(f Finding) string {
	if f.File == "" {
		return ""
	}
	return fmt.Sprintf(" (`%s:%d`)", f.File, f.Line)
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func statusMark(s string) string {
	return map[string]string{"pass": "✅", "fail": "❌", "skip": "⏭️", "error": "💥"}[s]
}
