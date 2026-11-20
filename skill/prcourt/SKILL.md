---
name: prcourt
description: Review a GitHub pull request with three agents in Rex panes (a reviewer that suggests changes, a tester that runs and writes unit, integration, contract, and recorded e2e tests, and a judge that weighs both and decides) using the prcourt CLI and its TUI. Use when the user asks to review, test, or judge a pull request with prcourt, with agents, or in Rex.
---

# prcourt

`prcourt run PR` checks out the pull request in a fresh worktree and opens a Rex tab: the prcourt TUI on the left, the reviewer and tester agents on the right. Each agent reads its instructions from the run directory and hands in its result with `prcourt submit`, which validates it. The judge starts by itself once the reviewer and tester have both submitted, and its verdict is written to `verdict.md` in the run directory. You start the run, keep the user informed, and report the verdict; the three agents do the reviewing.

## Check dependencies first

Before every run, check that prcourt and the tools it drives are installed, even if they were installed last time:

```sh
command -v prcourt || go install github.com/chrisdmacrae/prcourt@latest
prcourt doctor -agent claude
```

Pass `doctor` the same `-agent`, `-reviewer`, `-tester`, `-judge`, and `-no-rex` flags you will pass to `run`, so it checks the agents that run will start. It prints `FAIL` with a `fix:` command for each missing required tool (git, gh and its sign-in, rex and its server, ffmpeg and ffprobe, and the chosen agent CLIs), and `warn` for node and npx, which the tester needs for Playwright e2e tests. Install each failing tool with its fix command, telling the user what you are installing, then run `doctor` again until it reports no `FAIL`. Steps that need the user, such as `gh auth login`, signing in to claude or codex, or opening the Rex app, are theirs to do: ask them, and wait. `prcourt run` runs the same checks and stops with the same list if anything is still missing. If `go` itself is missing, install it the same way (`brew install go` on macOS).

## Start a run

Run it from the repository's local clone, or pass `-repo-dir`:

```sh
prcourt run 123 -json
```

PR is a number, URL, or branch. Useful flags:

- `-agent claude` (default) or `-agent codex` for every role; `-reviewer`, `-tester`, `-judge` override one role. `NAME:MODEL` picks a model, such as `codex:gpt-5.5`. When the user doesn't choose, run the judge on a different agent from the reviewer, such as `-judge codex`, so the judge does not share the reviewer's blind spots; mention that you did.
- `-yolo` lets the agents run commands without permission prompts. Pass it only when the user asks for it. Without it, agents stop at permission prompts, the TUI shows "needs you", and the user answers in that pane.
- `-repo owner/name` reviews a repository without a local clone; prcourt clones it into the run.

Inside Rex, the run opens as a new tab in the current session; elsewhere, as a new session named `pr-N-review`. `-json` prints run.json: save its `dir`.

If `rex` is missing or not running, `prcourt run -no-rex 123` prepares the run and prints each agent's command for the user to start.

## Follow the run

```sh
prcourt wait RUN_DIR -timeout 9m
```

It prints each stage change and returns when the judge has decided. A timeout does not mean the run failed; reviews with e2e tests take 10 to 40 minutes. Call it again, and between waits use `prcourt status RUN_DIR -json` to tell the user when an agent needs them: the TUI marks a pane that is blocked on a prompt or stopped without submitting. Tell the user which pane needs them; don't type into the agents' panes yourself.

When one agent cannot finish, the user can start the judge anyway with `prcourt judge RUN_DIR` or `J` in the TUI, after the other agent has submitted.

## Report the verdict

Read `RUN_DIR/verdict.md` (or `prcourt report RUN_DIR`) and tell the user the decision, the requirements that are unmet or unverified, the findings the judge upheld, how many it dismissed, and where the recordings are (`RUN_DIR/artifacts`, frames in `RUN_DIR/frames`). The tests the tester added are in `RUN_DIR/tests.patch` and are not on the PR yet. When that file is not empty, the tester's pane ends by asking the user whether to commit and push them to the PR branch: point the user to that question, and let the user answer it there.

`prcourt post RUN_DIR` posts the report to the PR as a comment review. It is visible to everyone on the PR, so run it only after the user asks you to post. Remove the checkout with `prcourt clean RUN_DIR` when the user is done; the reports and artifacts stay.
