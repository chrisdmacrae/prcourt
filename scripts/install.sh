#!/bin/sh
# Downloads prcourt from GitHub Releases and links the skill into the skill
# folders of Claude Code (~/.claude/skills) and Codex and other agents
# that read ~/.agents/skills.
set -eu
root=$(cd "$(dirname "$0")/.." && pwd)

sh "$root/skill/prcourt/scripts/install.sh"
bin="$HOME/.local/bin"
case ":$PATH:" in *":$bin:"*) ;; *) echo "Add $bin to PATH so agents can run prcourt." ;; esac

for dir in "$HOME/.claude/skills" "$HOME/.agents/skills"; do
  mkdir -p "$dir"
  ln -sfn "$root/skill/prcourt" "$dir/prcourt"
  echo "Linked the skill into $dir/prcourt"
done

for tool in gh git rex ffmpeg; do
  command -v "$tool" >/dev/null 2>&1 || echo "warning: $tool is not installed"
done
command -v claude >/dev/null 2>&1 || command -v codex >/dev/null 2>&1 || echo "warning: install claude or codex"
