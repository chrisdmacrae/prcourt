# prcourt

Review a GitHub pull request with three coding agents running side by side in [Rex](https://www.superlogical.com/rex), driven by a skill for Claude Code or Codex and watched from a Go TUI.

- **Reviewer** reads the PR description, linked issues, comments, and diff with `gh`, lists the requirements, and suggests changes.
- **Tester** learns how the repository tests, runs the relevant suites, adds unit, integration, contract, and e2e tests the way the repository writes them, and records every e2e run (video, screenshots, logs) for people to check.
- **Judge** rules on every reviewer finding (upheld, downgraded, pedantic, hallucinated, wrong), looks at the screenshots and the frames extracted from the videos, checks each requirement, and decides: approve, request changes, or needs a human.

```
┌──────────────┬───────────────┬──────────────┐
│ prcourt TUI  │ Reviewer      │ Judge        │
│ status,      │ (claude/codex)│ starts when  │
│ findings,    ├───────────────┤ both submit  │
│ tests,       │ Tester        │              │
│ verdict      │ (claude/codex)│              │
└──────────────┴───────────────┴──────────────┘
```

## Install

```sh
./scripts/install.sh      # go install, and link the skill into ~/.claude/skills and ~/.agents/skills
prcourt doctor            # checks git, gh (signed in), rex, ffmpeg, node, and the agent CLIs
```

## Use

From a coding agent with the skill: "review PR 123 with prcourt". Or by hand, in the repository's clone:

```sh
prcourt run 123                        # claude for every role
prcourt run 123 -judge codex           # a different model judges
prcourt run 123 -agent codex:gpt-5.5   # NAME:MODEL
prcourt run 123 -yolo                  # no permission prompts in the agents
prcourt wait  ~/.prcourt/runs/OWNER__REPO/pr-123-…   # until the verdict
prcourt report RUN                     # the verdict as Markdown
prcourt post RUN                       # post it as a comment review (never approves on GitHub)
prcourt clean RUN                      # remove the checkout, keep the evidence
```

Without `-yolo`, agents stop at their permission prompts and the TUI shows "needs you"; press `r`, `t`, or `g` to jump to that pane. The first run in a new directory also asks each agent CLI to trust the folder.

TUI keys: `1`–`5` or `tab` switch tabs, `j`/`k` move, `enter` details, `o` opens a test's video or screenshot or a finding's file in `$EDITOR`, `a` opens the artifacts folder, `J` starts the judge now, `q` quits.

## How it works

`prcourt run` checks out the PR head in a worktree of your clone (or a fresh clone with `-repo`), saves `pr.json`, `pr.diff`, and the inline review comments, and renders each role's instructions from `prompts/` into the run directory under `~/.prcourt/runs/`. Each agent writes a draft and hands it in with `prcourt submit ROLE`, which rejects anything malformed and lists every problem at once, so the agent can fix them and resubmit. On top of the JSON shape, submit enforces:

- Reviewer: every code finding names a file, a line at the PR head, and the exact quoted code. prcourt checks them against the checkout and rejects findings whose file, line, or quote does not exist, the commonest hallucination. It also tags findings on lines the PR does not change, a common source of pedantry, for the judge.
- Tester: every test has a command and a log; every e2e test that ran has a video or a screenshot; artifacts live inside the run directory. Frames are sampled from each video with ffmpeg for the judge, and the added tests are saved as `tests.patch`.
- Judge: every finding gets a ruling with a reason, every requirement a status with evidence, every test an assessment; a downgrade must lower the severity; and `approve` is refused while a blocker or major problem stands or a requirement is not met.

When the reviewer and tester have both submitted, the second `submit` starts the judge in a pane beside the reviewer. The judge's submit writes `verdict.md`.

## Develop

```sh
go test ./...
go run . run 123 -agent 'echo {prompt}; exec sleep 3600'   # stub agents, to test the layout without spending tokens
```
