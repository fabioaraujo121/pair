# PRD validation — Item 6: Packaging (Homebrew name collision, goreleaser tap deps)

Date: 2026-09-24. Host: macOS (Darwin 25.6.0), Homebrew 7.0.6, go1.27.1 darwin/arm64, goreleaser not installed locally (docs read online, v2.18 site).
Raw sources saved under `src/` next to this file (casks.txt, brew_formula.rb.tmpl, lazy_types.lua, pairdeptest*.rb).

## Summary of verdicts

| # | Claim | Verdict | PRD change |
|---|-------|---------|------------|
| a | `pair` does not collide on Homebrew / PATH | VERIFIED (Homebrew core+cask, PATH, man: nothing named `pair`) | Keep `pair`; note `brew search pair` is noisy; fallback names `pairvoice`, `pairtty` are clean everywhere |
| b1 | goreleaser v2 `brews:` deprecated in favor of `homebrew_casks:` | VERIFIED (soft-deprecated v2.10, deprecated v2.16) | "Homebrew formula" -> "Homebrew cask via goreleaser `homebrew_casks`" |
| b2 | Formula can declare `type: optional/recommended`; cask can `depends_on formula:` | VERIFIED (formula template passes `type` through; cask doc has `dependencies: - formula:`) | Hard deps as cask formula deps; git-delta/hammerspoon go to `caveats` (cask DSL has no optional/recommended) |
| c | `go install` fallback works with `cmd/pair` | VERIFIED | Spell it out: `go install github.com/<owner>/pair/cmd/pair@latest` |
| d | Dep name: `whisper-cpp` vs `whisper.cpp` | WRONG as written (formula renamed); old name still resolves via formula_renames | Use `whisper.cpp` in prereq table and in the tap deps |
| e | lazy.nvim `rtp` key pointing at `nvim/` | WRONG (no `rtp` key exists; maintainer closed as not planned) | Use `config`+`packadd` workaround snippet, or split `pair.nvim` into its own repo |
| — | "Homebrew bottle via goreleaser" (Target users section) | WRONG wording | goreleaser ships pre-built binaries in a tap cask/formula; it does not build bottles |

---

## a. Name collision on `pair`

Commands (all read-only):

```
$ brew search pair
zsh-autopair  par  air          <- formulae (fuzzy matches only)
idevice-pair  nvidia-pair  pairpods   <- casks
$ brew search --formula '/^pair/'   -> Error: No formulae or casks found for "/^pair/".
$ brew search --cask '/^pair/'      -> pairpods
$ brew info pair                    -> Error: No available formula with the name "pair". Did you mean par or air?
$ brew info --cask pair             -> Error: Cask 'pair' is unavailable: No Cask with this name exists.
$ command -v pair                   -> (nothing, exit 1)
$ man -w pair                       -> No manual entry for pair
$ ls /usr/bin/pair /bin/pair /usr/local/bin/pair /opt/homebrew/bin/pair -> all "No such file"
```

Remote checks:

```
https://formulae.brew.sh/api/formula/pair.json                          -> HTTP 404
https://formulae.brew.sh/api/cask/pair.json                             -> HTTP 404
https://raw.githubusercontent.com/Homebrew/homebrew-core/HEAD/Formula/p/pair.rb -> HTTP 404
https://raw.githubusercontent.com/Homebrew/homebrew-cask/HEAD/Casks/p/pair.rb   -> HTTP 404
formulae.brew.sh formula.json: names starting with "pair" -> []
formulae.brew.sh cask.json:    tokens starting with "pair" -> ['pairpods']
```

Other ecosystems (informational only; none ships a `pair` executable, so no PATH collision):

- npm `pair`: "Encode key/value pairs for LevelDB-style database storage in a Strata b-tree", `bin: None`.
- crates.io `pair` 0.2.0: "Safe API for generic self-referential pairs of owner and dependent" (library).
- PyPI `pair` 0.2.3: "a ladder pairing/matching library" (library).

Verdict: VERIFIED — no collision in homebrew-core, homebrew-cask, macOS PATH, or man pages. A tap-scoped `brew install <owner>/tap/pair` cannot collide with core anyway. Residual risk is discoverability (generic English word; `brew search pair` returns unrelated hits) and the theoretical case where core later adds a `pair` formula, which would make a bare `brew install pair` resolve to core instead of the tap.

Alternative names checked (formula/cask on formulae.brew.sh, npm, crates.io, PyPI, PATH):

| name | brew formula | brew cask | npm | crates | PyPI | PATH |
|------|---|---|---|---|---|---|
| pairvoice | 404 | 404 | 404 | 404 | 404 | none |
| pairtty   | 404 | 404 | 404 | 404 | 404 | none |
| pairup    | 404 | 404 | **200 (taken)** | 404 | 404 | none |
| copair / voicepair / pairing / pairwise | 404 | 404 | not checked | | | |

Suggestion: keep `pair` (collision risk is not real for a tap); if renaming for searchability, `pairvoice` and `pairtty` are clean on every registry checked.

## b. goreleaser: formula vs cask, dependency declarations

### b1. Is `brews:` deprecated?

https://goreleaser.com/deprecations/ — entry "brews":

> Since: v2.10 (soft) since v2.16
> "Historically, GoReleaser would generate _hackyish_ formulas that would install the pre-compiled binaries. This was the only way to do it for Linuxbrew at the time, but this is no longer true, and _Casks_ should be used instead."
>
> ```yaml
> # Before
> brews:
>   - name: foo
>     directory: Formulas
> # After
> homebrew_casks:
>   - name: foo
>     directory: Casks
>     hooks:
>       post:
>         install: |
>           if OS.mac?
>             system_command "/usr/bin/xattr", args: ["-dr", "com.apple.quarantine", "#{staged_path}/foo"]
>           end
> ```

https://goreleaser.com/customization/homebrew_formulas/ top admonition: "Deprecated in v2.10. Homebrew Casks should be used instead."
https://goreleaser.com/customization/homebrew_casks/ : "Since v2.10." The site nav lists the formula page as "Homebrew (deprecated)".

Verdict: VERIFIED. `brews:` still works in v2.18 but emits deprecation warnings and is slated for removal.

### b2. Can a formula declare optional/recommended deps? Can a cask declare formula deps?

Formula (deprecated `brews:`) docs, `dependencies` block, quoted from https://goreleaser.com/customization/homebrew_formulas/ :

```yaml
dependencies:
  - name: git
    # Allow to specify the OS in which the dependency is required.
    # Valid options are `mac` and `linux`.
    os: mac
  - name: zsh
    type: optional
  - name: fish
    version: v1.2.3
  # if providing both version and type, only the type will be taken into
  # account.
  - name: elvish
    type: optional
    version: v1.2.3
```

goreleaser source, `internal/pipe/brew/templates/formula.rb` (lines 16-21), shows `type` is passed straight through as a Ruby symbol, so `recommended` (a valid Homebrew tag) works too:

```
  {{- with .Dependencies }}
  {{ range $index, $element := . }}
  depends_on "{{ .Name }}"
  {{- if .Type }} => :{{ .Type }}{{- else if .Version }} => "{{ .Version }}"{{- end }}
  {{- with .OS }} if OS.{{ . }}?{{- end }}
  {{- end }}
  {{- end -}}
```

i.e. `- name: git-delta` + `type: recommended` renders `depends_on "git-delta" => :recommended`.

Cask docs, quoted from https://goreleaser.com/customization/homebrew_casks/ :

```yaml
    # Dependencies for the cask.
    dependencies:
      - cask: some-cask
      - formula: some-formula
    # Packages that conflict with your cask.
    conflicts:
      - cask: some-cask
```

and

```yaml
    # Binary name inside the cask
    #
    # Default: the cask name.
    # Templates: allowed.
    binaries:
      - myapp
      - myapp2
```

Homebrew Cask DSL confirms the stanza (https://docs.brew.sh/Cask-Cookbook, `depends_on formula:`): "one or more names of formulae needed by the current cask, as a string or array of strings", example `depends_on formula: "unar"`. Local copy `src/cask_cookbook.md` line 447. The Cask DSL has no optional/recommended dependency tags, so git-delta/hammerspoon must be advertised via `caveats` in a cask.

Signing caveat (same goreleaser cask page, "Signing and Notarizing"):

> "Casks are supposed to be signed, even if they are coming from a tap. GoReleaser can sign and notarize both binaries and apps, but Apple charges a yearly fee for that. If you don't want to do it, you still have the option to tell macOS to remove the quarantine bit from the binary on a post install hook" ... "Not following this might lead to your app or binary not running. In these cases, users will see the infamous 'App Name is damaged and cannot be opened' alert."

Verdict: VERIFIED for both. Choose **cask**: it is the non-deprecated goreleaser path and `dependencies: - formula:` covers the four hard runtime deps; the only losses are `:recommended` semantics (use `caveats`) and the need for either notarization or the xattr post-install hook.

### Minimal `.goreleaser.yaml` (darwin arm64/amd64 + tap cask with deps)

```yaml
# .goreleaser.yaml — goreleaser v2.18
version: 2
project_name: pair

builds:
  - id: pair
    main: ./cmd/pair
    binary: pair
    env: [CGO_ENABLED=0]
    goos: [darwin]
    goarch: [amd64, arm64]
    ldflags: ["-s -w -X main.version={{ .Version }}"]

archives:
  - id: default
    formats: [tar.gz]
    name_template: "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}"

homebrew_casks:
  - name: pair
    binaries: [pair]
    homepage: https://github.com/<owner>/pair
    description: Voice-driven pairing for Claude Code in the terminal
    license: MIT
    directory: Casks
    repository:
      owner: <owner>
      name: homebrew-tap
      token: "{{ .Env.HOMEBREW_TAP_GITHUB_TOKEN }}"
    dependencies:
      - formula: whisper.cpp
      - formula: sox
      - formula: tmux
      - formula: neovim
    caveats: |
      Recommended: brew install git-delta; brew install --cask hammerspoon
      whisper.cpp ships no model: run `pair doctor` to download one.
    hooks:
      post:
        install: |
          if OS.mac?
            system_command "/usr/bin/xattr", args: ["-dr", "com.apple.quarantine", "#{staged_path}/pair"]
          end
```

If `:recommended` semantics matter more than staying on the supported path, the deprecated formula form is:

```yaml
brews:
  - name: pair
    directory: Formula
    repository: { owner: <owner>, name: homebrew-tap }
    dependencies:
      - name: whisper.cpp
      - name: sox
      - name: tmux
      - name: neovim
      - name: git-delta
        type: recommended   # renders: depends_on "git-delta" => :recommended
```

Not run locally (goreleaser not installed; no brew installs allowed during this check). Validate with `goreleaser check` once the repo exists.

## c. `go install` fallback

`go help build`: "…an output file named after the last non-major-version component of the package import path." `go help install`: with `@latest` it "builds packages in module-aware mode, ignoring the go.mod file in the current directory".

So `go install github.com/<owner>/pair/cmd/pair@latest` produces `$GOBIN/pair`. Only caveats: module path must be `github.com/<owner>/pair` (root `go.mod`), tag releases (`v0.x.y`) so `@latest` is meaningful, and keep the binary cgo-free (MCP go-sdk and neovim/go-client are pure Go) — which the snippet above enforces with `CGO_ENABLED=0`. Verdict: VERIFIED.

## d. Dependency name: `whisper-cpp` vs `whisper.cpp`

```
$ brew info whisper.cpp
==> whisper.cpp: stable 1.9.4 (bottled), HEAD
Old Names: whisper-cpp
From: https://github.com/Homebrew/homebrew-core/blob/HEAD/Formula/w/whisper.cpp.rb
==> Dependencies  Required (3): ggml, llama.cpp, sdl2-compat
==> Caveats
whisper.cpp requires GGML model files to work. These are not downloaded by default.
To obtain model files (.bin), visit one of these locations:
  https://huggingface.co/ggerganov/whisper.cpp/tree/main
  https://ggml.ggerganov.com/

$ brew info whisper-cpp        # old name resolves to the same formula
==> whisper.cpp: stable 1.9.4 (bottled), HEAD
```

homebrew-core `formula_renames.json` line 185: `"whisper-cpp": "whisper.cpp",`

Homebrew resolution path (`/opt/homebrew/Library/Homebrew/dependency.rb:56-57`): `def to_formula; formula = Formulary.factory(name, warn: false)`; and `formulary.rb:1192-1196`:

```ruby
    elsif (new_name = tap.formula_renames[name].presence)
      old_name = tap.core_tap? ? name : tapped_name
      name = new_name
      ...
      type = :rename
```

So a `depends_on "whisper-cpp"` in a tap formula or `depends_on formula: "whisper-cpp"` in a cask resolves to `whisper.cpp` today, silently (`warn: false`). Use the new name anyway; renames are a compatibility shim.

Binaries installed by the formula (`ls /opt/homebrew/opt/whisper.cpp/bin`, all symlinked into `/opt/homebrew/bin`):
`whisper-cli`, `whisper-bench`, `whisper-command`, `whisper-lsp`, `whisper-quantize`, `whisper-server`, `whisper-stream`, `whisper-talk-llama`, `whisper-vad-speech-segments`, `parakeet-cli`, `parakeet-quantize`.
The formula's own test runs `#{bin}/whisper-cli --model … jfk.wav`, so `whisper-cli` is the stable name the PRD relies on.

Model: NOT included. `pkgshare.install "models/for-tests-ggml-tiny.bin", "samples/jfk.wav"` puts only a 575 KB test model at `/opt/homebrew/opt/whisper.cpp/share/whisper.cpp/for-tests-ggml-tiny.bin` (handy for a `pair doctor` smoke test, useless for real STT). `pair doctor` must download `ggml-large-v3-turbo.bin` (or similar) itself.

Note: `whisper-stream` (SDL2 mic capture, built with `-DWHISPER_SDL2=ON`) exists as a possible alternative to `sox rec` + `whisper-cli` for push-to-talk; out of scope here but worth a look in item 4.

Verdict: PRD text `brew install whisper-cpp` is WRONG (stale name, still works via rename); dependency should be `whisper.cpp`.

## e. lazy.nvim: subdirectory of a repo

lazy.nvim spec (https://lazy.folke.io/spec and `lua/lazy/types.lua`): fields are `[1]`, `name`, `main`, `url`, `dir`, `enabled`, `cond`, `optional`, `lazy`, `priority`, `dev`, `rocks`, `virtual`, `dependencies`, `specs`, `init`, `deactivate`, `config`, `build`, `opts`, `event`, `ft`, `keys`, `cmd`, `branch`, `tag`, `commit`, `version`, `pin`, `submodules`. There is **no `rtp` (or subdir) key**. `dir` is "A directory pointing to a local plugin" — a local path, not a repo subfolder.

Maintainer position, folke on issue #183 (closed 2022-12-26):

> I just pushed a change so you can do this:
> ```lua
>   {
>     "sonph/onehalf",
>     lazy = false,
>     config = function(plugin)
>       vim.opt.rtp:append(plugin.dir .. "/vim")
>     end,
>   },
> ```
> Not planning on adding any other type of rtp support.

and, for lazy-loaded/ft plugins: "After appending to the rtp, add … `require("lazy.core.loader").packadd(<the rtp path>)`" and "add an `init()` function where you do: `require("lazy.core.loader").ftdetect(plugin.dir .. "/editor-support/vim")`".
Issue #756 "feature: equivalent to Packer `rtp` option" closed as **not_planned** (2023-04-26): "Not going to add this. Check related issue for a work-around".

Working spec for the PRD layout (`nvim/` inside the `pair` repo):

```lua
{
  "<owner>/pair",
  lazy = false,
  config = function(plugin)
    vim.opt.rtp:append(plugin.dir .. "/nvim")
    require("lazy.core.loader").packadd(plugin.dir .. "/nvim")
    require("pair").setup()
  end,
}
```

Downsides: lazy.nvim first adds the repo root to rtp (Go sources, templates) and runs the repo-root `plugin/` dir, and users of packer/vim-plug/rocks get a different story. Cleaner: a separate `pair.nvim` repo (the PRD already lists this as an option). Verdict: WRONG as written; amend either to the snippet above or to the separate-repo option.

## PRD amendments (concrete)

1. Prerequisites table: `brew install whisper-cpp` -> `brew install whisper.cpp`; add "download a model (`pair doctor` fetches it)" — the formula ships none.
2. Target users section: "a Homebrew bottle via goreleaser" -> "pre-built darwin binaries published to a Homebrew tap via goreleaser" (goreleaser does not build bottles).
3. Installation/distribution: "Distribute as a Homebrew formula" -> "Homebrew cask (goreleaser `homebrew_casks`; `brews:` is deprecated since v2.10/v2.16)". Hard deps `whisper.cpp`, `sox`, `tmux`, `neovim` via `dependencies: - formula:`; git-delta and hammerspoon via `caveats` (cask DSL has no recommended deps). Add a post-install `xattr -dr com.apple.quarantine` hook or budget Apple notarization.
4. Neovim plugin line: replace "`rtp` set to `nvim/`" with the `config`/`packadd` snippet, or commit to a separate `pair.nvim` repo.
5. `go install` line: `go install github.com/<owner>/pair/cmd/pair@latest`; keep `CGO_ENABLED=0`.
6. Risks table: add "unsigned cask binary blocked by Gatekeeper" (mitigation: xattr hook now, notarization later) and, optionally, the name-discoverability note.
7. Open question "Name": collision risk is not real for a tap; `pairvoice` / `pairtty` are clean fallbacks if renamed for searchability.
