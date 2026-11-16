#!/bin/sh
# Publish nvim/ to the pair.nvim repository from this machine, with its
# history. The nvim workflow does the same on every push to main; this is
# the fallback when it cannot run. Needs `gh auth login`.
set -eu
cd "$(dirname "$0")/.."
git subtree split --prefix=nvim -b pair-nvim >/dev/null 2>&1
git push "https://x-access-token:$(gh auth token)@github.com/fabioaraujo121/pair.nvim.git" pair-nvim:main
git branch -D pair-nvim >/dev/null
echo "pair.nvim is at $(git log -1 --format=%h -- nvim) of nvim/"
