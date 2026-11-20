---
name: prcourt
description: Review a GitHub pull request with a reviewer, a tester, and a judge using the prcourt CLI and its TUI, with Rex panes when available. Use when the user asks to review, test, or judge a pull request with prcourt, with agents, or in Rex.
---

# prcourt

`prcourt run PR` checks out the pull request in a fresh worktree. When Rex is available, it opens a Rex tab: the prcourt TUI on the left, the reviewer and tester agents on the right. Otherwise, use your host's agent tools or separate terminals to run the roles. Each agent reads its instructions from the run directory and hands in its result with `prcourt submit`, which validates it. In Rex, the judge starts by itself once both agents submit; without Rex, you start it after both submit. Its verdict is written to `verdict.md`. You start the run, keep the user informed, and report the verdict; the three agents do the reviewing.

## Check dependencies first

Before every run, check for CLI updates using the installer bundled with this skill. It queries the latest stable GitHub Release, compares its tag with `prcourt --version`, and downloads the matching binary only when missing or different. Downloads are SHA-256 checked before replacing the CLI. Go is not required. Install GitHub CLI (`gh`, https://cli.github.com) first if missing.

On macOS or Linux, prepend the user bin directory to PATH and run `scripts/install.sh` relative to this skill's directory:

```sh
export PATH="$HOME/.local/bin:$PATH"
sh /path/to/this/skill/scripts/install.sh
prcourt doctor -agent claude
```

On Windows, use PowerShell 7 and the bundled `scripts/install.ps1`:

```powershell
$env:PATH = "$HOME/.local/bin;" + $env:PATH
& /path/to/this/skill/scripts/install.ps1
prcourt doctor -agent claude
```

Resolve the script path from the loaded skill's location, not the repository being reviewed. Both scripts install under `~/.local/bin` and support x64 and arm64; Windows uses `prcourt.exe`. Keep that directory first in PATH for the agent commands that follow. If checking or downloading fails, report the error and use the existing CLI if it still works; if none is installed, resolve the failure before starting. A repository with no published stable release cannot supply a binary yet. Do not install Go as a fallback.

To check the installed version yourself:

```sh
prcourt --version
```

Pass `doctor` the same `-agent`, `-reviewer`, `-tester`, `-judge`, and `-no-rex` flags you will pass to `run`. It prints `FAIL` for missing required tools (git, gh and its sign-in, ffmpeg and ffprobe, and the chosen agent CLIs), and `warn` for optional Rex, terminal-browser, node, and npx. Resolve required failures and run `doctor` again. Steps such as `gh auth login` or signing in to claude or codex need the user. Do not install Rex or ask the user to open it just to run court: missing Rex or an unreachable server selects the no-Rex path automatically. `prcourt run` repeats these checks at startup.

When this skill is used from a terminal-based agent TUI, check the `terminal-browser` result at startup. If installed, pass `-terminal-browser` to `run` so the tester uses it for interactive browser checks and load its skill when you need to drive a browser yourself. In a non-terminal host, omit that flag. A missing terminal-browser is not a blocker; keep the existing browser and recording tools. Repository-owned e2e suites still run with their own framework.

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

If `rex` is missing or not running, the run is still prepared. `-no-rex` forces this mode even when Rex is available. `-json` prints the same run metadata in either mode; an empty `rex.session` means no panes were launched. Start the reviewer and tester with your host's agent tools, each in `worktree`, reading `prompts/reviewer.md` or `prompts/tester.md` under `dir` and setting `PRCOURT_RUN=dir`. If the host has no agent tools, run without `-json` to get commands for separate terminals. After both submit, start the judge from `prompts/judge.md` the same way, or use `prcourt judge RUN_DIR` to print its command. Do not wait for a verdict before starting these agents. The user can watch progress with `prcourt tui RUN_DIR` in any terminal.

## Follow the run

```sh
prcourt wait RUN_DIR -timeout 9m
```

It prints each stage change and returns when the judge has decided. A timeout does not mean the run failed; reviews with e2e tests take 10 to 40 minutes. Call it again, and between waits use `prcourt status RUN_DIR -json` to tell the user when an agent needs them: the TUI marks a pane that is blocked on a prompt or stopped without submitting. Tell the user which pane needs them; don't type into the agents' panes yourself.

When one agent cannot finish, the user can start the judge anyway with `prcourt judge RUN_DIR` or `J` in the TUI, after the other agent has submitted.

## Report the verdict

Read `RUN_DIR/verdict.md` (or `prcourt report RUN_DIR`) and tell the user the decision, the requirements that are unmet or unverified, the findings the judge upheld, how many it dismissed, and where the recordings are (`RUN_DIR/artifacts`, frames in `RUN_DIR/frames`). The tests the tester added are in `RUN_DIR/tests.patch` and are not on the PR yet. When that file is not empty, the tester's pane ends by asking the user whether to commit and push them to the PR branch: point the user to that question, and let the user answer it there.

`prcourt post RUN_DIR` posts the report to the PR as a comment review. It is visible to everyone on the PR, so run it only after the user asks you to post. Remove the checkout with `prcourt clean RUN_DIR` when the user is done; the reports and artifacts stay.
