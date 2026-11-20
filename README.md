# prcourt

Review GitHub pull requests with three coding agents: a reviewer, a tester, and a judge. Use the skill in Claude Code or Codex and follow progress in a terminal UI.

## Install

Install the skill for both Claude Code and Codex with the [skills CLI](https://skills.sh), without manually cloning or keeping a checkout:

```sh
npx skills add https://github.com/chrisdmacrae/prcourt/tree/main/skill/prcourt --global --agent claude-code --agent codex
```

This command requires Node/npm. Omit either `--agent` option to install for just one agent, or omit `--global` for a project-local installation.

**No Go installation is needed.** Before each review, the skill checks for the latest stable [CLI release](https://github.com/chrisdmacrae/prcourt/releases/latest) and installs or updates the binary under `~/.local/bin`, verifying its SHA-256 checksum. macOS, Windows, and Linux are supported on x64 and arm64. Windows installation uses PowerShell 7.

Court also needs [GitHub CLI](https://cli.github.com) signed in, Git, ffmpeg/ffprobe, and the selected agent CLIs. The skill runs `prcourt doctor` to check these dependencies. Rex and terminal-browser are optional.

Update the skill's instructions and bundled scripts separately:

```sh
npx skills update prcourt --global
```

## Use

In Claude Code or Codex, ask:

> Review PR 123 with prcourt.

The skill prepares an isolated checkout, starts the agents, and reports the verdict. If [Rex](https://www.superlogical.com/rex) and its server are available, the agents and progress UI open in panes. Otherwise, the skill uses the host's agent tools or supplies commands for separate terminals.

From the CLI, run in the repository being reviewed:

```sh
prcourt doctor
prcourt run 123                       # Claude for every role
prcourt run 123 -judge codex          # choose a different judge
prcourt run 123 -agent codex:gpt-5.5  # select an agent and model
prcourt tui RUN                      # watch progress in any terminal
prcourt wait RUN                     # wait for the verdict
prcourt report RUN                   # print the Markdown report
prcourt post RUN                     # post a comment review to GitHub
prcourt clean RUN                    # remove the checkout, keep the evidence
```

`RUN` is the directory printed by `prcourt run`, under `~/.prcourt/runs/` by default. Add `~/.local/bin` to PATH to use the downloaded CLI directly. `prcourt --version` prints the installed release tag.

Useful run flags:

- `-repo owner/name`: review a repository without a local clone.
- `-no-rex`: prepare the run and print agent commands without opening panes.
- `-json`: print run metadata for automation, with or without Rex.
- `-terminal-browser`: use terminal-browser for interactive browser checks if installed. The skill selects this in terminal-based hosts; repository-owned e2e suites keep their existing framework.
- `-yolo`: bypass agent permission prompts and sandboxes. Without it, answer prompts in the agent's terminal; the Rex UI marks blocked panes as "needs you".

TUI keys: `1`–`5` or `tab` switch tabs; `j`/`k` move; `enter` shows details; `o` opens evidence or a finding in `$EDITOR`; `a` opens artifacts; `r`/`t`/`g` focus Rex agent panes; `J` requests the judge; `q` quits.

## How it works

The PR head is checked out in a separate worktree or clone, leaving your working directory untouched. The run directory holds the PR metadata, diff, review comments, agent prompts, and evidence.

1. **Reviewer** reads the PR, linked issues, comments, and code, then lists requirements and findings. Court checks cited files, lines, and quotes against the checkout and flags findings outside the diff.
2. **Tester** follows the repository's testing conventions, runs relevant suites, adds missing tests, and records e2e evidence. Every test needs a command and log; every executed e2e test needs a video or screenshot. Added tests are saved as `tests.patch` and are committed or pushed only with your approval.
3. **Judge** checks each finding, requirement, test, and visual artifact. It dismisses unsupported or pedantic findings and decides `approve`, `request_changes`, or `needs_human`. Court rejects approval while a blocker or major issue stands or a requirement remains unmet or unverified.

Each agent submits validated JSON with `prcourt submit ROLE`. Once both reviewer and tester submit, court starts the judge in Rex or prints its command; without Rex, the skill can start it through the host's agent tools. `prcourt judge RUN` also provides the judge command. The final report is `verdict.md`; recordings and sampled video frames remain in the run directory.

Court does not automatically post reviews or approve PRs on GitHub. `prcourt post` publishes a comment review only when you ask it to.

## Develop

Clone the repository and use the Go version in `go.mod`:

```sh
go test ./...
go install .
go run . run 123 -agent 'echo {prompt}; exec sleep 3600'  # test the Rex layout with stub agents
```

Alternatively, `./scripts/install.sh` downloads the release CLI and links this checkout's skill into Claude Code and Codex without building from source.

## Releases

Pushing a version tag such as `v0.1.0` triggers [the release workflow](.github/workflows/release.yml): tests and vet, six CGO-free builds, and a GitHub Release containing the binaries and `checksums.txt`. Binaries embed the tag as their version. Assets use `prcourt_<darwin|windows|linux>_<amd64|arm64>`, with `.exe` on Windows.

Tags containing a hyphen, such as `v0.2.0-rc.1`, publish prereleases that the installers skip. Ordinary branch pushes do not publish releases.
