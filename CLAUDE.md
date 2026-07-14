# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

Chrest is a CLI tool and browser extension that enables managing Chrome/Firefox via REST. It consists of:

1. **Browser Extension** (`extension/`) - JavaScript service worker that exposes browser APIs as REST endpoints
2. **Native Messaging Host & CLI** (`go/`) - Go binary that communicates with the extension via Chrome Native Messaging

## Build Commands

All commands use justfiles. Run from appropriate directory or use root justfile.
Ad-hoc debug/exploration recipes live under `[group: 'explore']` in the root
justfile — prefer adding recipes there over writing one-off shell scripts.

### Full Build

Recipe taxonomy follows `eng-design_patterns-justfile(7)`: verb-noun
names, lifecycle groups (`pre-build`, `build`, `post-build`, etc.),
aggregate-only entry recipes (`validate`, `lint`, `build`, `verify`, `test`).

```bash
just                                     # default: validate lint build verify test (sweatfile pre-merge surface)
just build                               # aggregate: build-nix
just build-nix                           # `nix build --no-link` (chrest derivation: three binaries + Go unit suite in checkPhase)
just validate                            # validate-devshell + validate-dagnabit-export + validate-dagnabit-reposition
just validate-devshell                   # builds .#devShells.<arch>-linux.default; catches devshell-only regressions
just validate-dagnabit-export            # drift gate: go/pkgs/ matches what `dagnabit export` would generate
just validate-dagnabit-reposition        # drift gate: go/internal/<level>/<leaf> matches `dagnabit reposition` depth
just lint                                # aggregate: lint-fmt + lint-doppelgang
just lint-fmt                            # builds checks.treefmt (read-only treefmt gate; codemod-fmt-treefmt is the modifier)
just lint-doppelgang                     # `doppelgang lint --flake . --no-closure` (flake.lock dedup gate; see chrest#87)
just codemod-fmt                         # aggregate: codemod-fmt-treefmt
just codemod-fmt-treefmt                 # `nix fmt` (treefmt-nix wrapper; rewrites the worktree)
just load-extension                      # nix-builds chrest, reinstalls native-messaging manifest, reloads extension
just verify                              # aggregate: verify-nix
just verify-nix                          # `nix flake check` after build; forces the flake-input-go_mod IFD (post-build, not validate)
just test                                # test-mcp + test-mcp-bats (unit tests already ran in checkPhase)
just test-mcp                            # validates MCP tools, resources, and annotations
just test-mcp-bats                       # BATS integration suite against a real unix socket
just build-go                            # devshell-only rapid iteration: cd go && go build -o build/release/ ./cmd/...
just test-go [flags]                     # devshell-only rapid iteration: cd go && go test {{flags}} ./...
just build-gomod2nix                     # manual maint: regenerate gomod2nix.toml after a go.mod change
just build-dagnabit-export               # regenerate go/pkgs/<leaf>/main.go facades from //go:generate dagnabit export directives
just codemod-dagnabit-reposition apply   # apply dagnabit's computed NATO retiering (drop `apply` for dry-run)
just install-mcp-dev                     # build + install MCP server to ~/.claude.json
just build-demo                          # generate VHS demo GIF
just deploy-tag <version> <message>      # sign + push a go/v<version> tag
just deploy-release <version>            # bump-version, commit, push master, then deploy-tag
just bump-version <version>              # sed-rewrite chrestVersion in flake.nix
```

`test-mcp-bats` is wall-clock bounded (360s timeout, per-lane; bumped from
180s for chrest#51 firefox-lane headroom) and validates success by
parsing TAP output — bats has been observed to hang on shutdown in bwrap
`--unshare-pid` sandboxes even when every test passes. Root cause still open.

`sweatfile` wires `pre-merge = "just"` — spinclass merge runs the full suite
before merging a worktree branch back to master. It also wires
`pre-commit = "conformist-pre-commit"` (chrest#105, chrest#106): the
conformist hook regenerates and stages go/pkgs/ dagnabit facades on every
commit that touches flake.lock or go/\*_/_.go. On flake.lock commits it
builds dagnabit from the staged lock via `nix build .#dagnabit` (chrest#106)
so purse-first bumps self-heal instead of failing the
validate-dagnabit-export gate.

The chrest derivation (`flake.nix`) builds three binaries — `chrest` (main
CLI + native messaging host + MCP server), `chrest-server`, and `chrest-jcs`
(standalone JCS canonicalizer for cross-implementation byte-stability
fixtures) — and runs the Go unit suite in `checkPhase` (with `HOME=$TMPDIR`
so pdfcpu's config-dir creation succeeds in the sandbox). A clean
`nix build` therefore proves both compile and unit tests in one step.

### Versioning (chrest#61)

`chrestVersion` in `flake.nix` is the single source of truth. It propagates to:

- Go binary `chrest version` — injected via `-X main.version`.
- MCP `serverInfo.version` — surfaces `app.Version` from the binary.
- Extension `manifest.json` `version` — templated by `extension/default.nix`
  at build time (overrides the source-tree static value).

Clean release builds (tagged commit, `self.shortRev` set) report bare
`X.Y.Z`. Dev / dirty builds report `X.Y.Z-dev+<shortSha>` (Go + MCP only;
the extension always uses the bare value because browser stores require
numeric-only semver).

Releases push two parallel tags pointing at the same commit:
`vX.Y.Z` (project-level canonical) and `go/vX.Y.Z` (path-prefix tag
preserved so downstream Go module consumers — e.g. dodder — can
`require code.linenisgreat.com/chrest/go vX.Y.Z`). `just deploy-release`
handles both.

`dewey` is consumed as the upstream module
`github.com/amarbel-llc/purse-first/libs/dewey` — chrest imports its
`pkgs/<leaf>` facades (e.g. `pkgs/errors`, `pkgs/ohio`, `pkgs/command`).
No vendored copy lives in this repo; bumping the pinned version is a
normal `go get` + `just build-gomod2nix` cycle.

Adding a Go dependency: from inside the nix devshell, `just go/add-dep
<pkg>` (or hand-edit `go/go.mod` + `go mod tidy`), then `just build-gomod2nix`
to regenerate `go/gomod2nix.toml`. Stage `go.mod`, `go.sum`, and
`gomod2nix.toml` together. `nix build` is the drift signal — it fails
loudly if the manifest is out of sync — so there is no longer a justfile-
level drift-guard recipe.

### Go (from `go/` directory)

```bash
just test-go            # run tests: go test -v ./...
just lint               # lint-go-vuln + lint-go-vet (govulncheck and go vet)
just codemod-fmt-go     # format with goimports and gofumpt (verb-nesting per eng-design_patterns-justfile(7))
just update-go          # update dependencies
just add-dep <pkg>      # go get <pkg> + go mod tidy
```

Builds (`nix build`, `just build-go`) and `build-gomod2nix` maintenance live in
the top-level justfile so they are discoverable without `cd go`.

### Extension (from `extension/` directory)

```bash
just build              # builds both chrome and firefox
just build-chrome       # builds chrome extension to dist-chrome/
just build-firefox      # builds firefox extension to dist-firefox/
just deploy-firefox     # sign and deploy to Firefox AMO
```

The extension build is a Nix derivation — `extension/default.nix` driven by
`pkgs.mkBunDerivation` (chrest#49). Each `just build-<browser>` invokes
`nix build .#extension-<browser>` and copies `dist-<browser>/` and
`dist-<browser>.zip` out of the `result-*` symlink for use by
`deploy-firefox` and manual browser loads. Two consecutive builds produce
byte-identical zips (mtime-normalized via `touch -t 198001010000`; Info-ZIP
does not honor `SOURCE_DATE_EPOCH`).

Dependencies are pinned in `extension/bun.lock` (bun) and mirrored to
`extension/bun.nix` (consumed by `fetchBunDeps`). To bump a dependency:
edit `package.json`, run `bun install`, then `bun2nix -l bun.lock -o
bun.nix`. Both files must be staged for `nix build` to see them
(dirty-tree builds only include git-tracked files).

## Architecture

### Communication Flow

1. CLI sends HTTP requests to Unix socket (`$XDG_STATE_HOME/chrest/<browser-id>.sock`)
2. Go server (`go/internal/*/server/`) forwards requests to browser extension via Native Messaging
3. Extension (`extension/src/main.js`) routes requests to handlers and returns HTTP responses
4. Extension uses mutex to serialize requests

### Public Go API (`go/pkgs/`)

Generated by dagnabit from `//go:generate dagnabit export` directives
in `go/internal/`. External consumers (e.g. dodder) import via
`code.linenisgreat.com/chrest/go/pkgs/<leaf>`; the leaf name is
stable across NATO-tier moves. Currently exported leaves:

- `pkgs/browser_items` — facade for `*/browser_items`, used by
  dodder's `store_browser`.
- `pkgs/client` — facade for `*/client`, used by dodder's
  `local_working_copy/format_chrest.go`.

Add a new public leaf by placing `//go:generate dagnabit export` in
the package's `main.go` and running `just build-dagnabit-export`. The
`just validate-dagnabit-export` recipe (in the `validate:` aggregator)
fails the build if `pkgs/` falls behind `internal/`. Similarly,
`just validate-dagnabit-reposition` fails if `internal/<level>/<leaf>`
no longer matches dagnabit's computed dependency height — run
`just codemod-dagnabit-reposition apply` to retier.

### Go Package Structure (`go/internal/`)

Packages are referenced by their **leaf** name, written as `*/<leaf>`,
because dagnabit reposition moves leaves across NATO levels as their
dependency graph shifts. Pinning the level here creates lies the
moment `just codemod-dagnabit-reposition apply` runs.

- `*/browser` - Browser detection utilities
- `*/symlink` - Symlink handling
- `*/client` - HTTP client for browser proxy communication
- `*/server` - Unix socket HTTP server, Native Messaging protocol
- `*/config` - Configuration and state directory management
- `*/bidi` - WebDriver BiDi transport. Background `readLoop` owns all
  reads; routes response frames to per-request channels and fans events out
  to `Subscribe(methods)` consumers. Prerequisite for capture envelope `http.*`
  fields (chrest#24).
- `*/install` - Native messaging host installation (platform-specific paths)
- `*/browser_items` - Browser item types and operations
- `*/firefox` - Firefox/BiDi capture backend. Subscribes to
  `network.responseCompleted`, drains stale events before each navigate, and
  populates `LastNavigationHTTP()` — enables envelope v1 emission. Also holds
  shared capture types (`HTTPResponse`, `HTTPHeader`, `PDFOptions`,
  `ScreenshotOptions`, `BrowserInfo`).
- `*/launcher` - Browser process launching.
- `*/monolith` - Shells out to the `monolith` CLI to inline every asset
  as `data:` URIs. Used by the `html-monolith` capture format; binary is wrapped
  into PATH via `flake.nix` postFixup.
- `*/markdown` - Pure-Go HTML-to-markdown encoding for the `markdown-*`
  capture formats. Wraps `JohannesKaufmann/html-to-markdown/v2` plus
  `codeberg.org/readeck/go-readability/v2` (for the reader variant) and
  `andybalholm/cascadia` (for the selector variant).
- `*/rawfetch` - Content-type classification + raw-text content
  building for the `capture` MCP tool's markdown/text/html dispatcher.
  `Classify` decides HTML / Text /
  Binary / HTTPError from response headers + URL ext + status;
  `BuildFromText` builds the text/markdown/html slots from a raw text body;
  `ExtractMarkdownTOCFromText` regex-scans markdown for ATX headings.
- `*/websearch` - PARKED skeleton for a web-search MCP tool (chrest#93:
  full plan, verified DDG SERP fixtures, bot-challenge findings). Nothing
  imports it yet.
- `*/proxy` - Multi-browser proxy (fan-out requests to all sockets)
- `*/tools` - MCP tool definitions with annotations
- `*/resources` - MCP paginated resources (`chrest://items`, `chrest://items/{page}`)
- `*/capturebatch` - See "Capture Pipeline" below.

### CLI Commands (`go/cmd/chrest/main.go`)

- `chrest` (default) - Start native messaging server
- `chrest client` - Forward HTTP request from stdin to browser
- `chrest install <extension-id>` - Install native messaging host manifest
- `chrest install-mcp` - Install MCP server config to `~/.claude.json`
- `chrest reload-extension` - Reload the browser extension
- `chrest items-get` / `items-put` - Get/put browser items
- `chrest init` - Initialize configuration (browser, name, extension-id)
- `chrest mcp` - Start MCP server (stdio transport)
- `chrest capture --format <kind>` - Single-page capture. Formats: `pdf`,
  `screenshot-png`, `screenshot-jpeg`, `mhtml`, `a11y`, `text`, `html-monolith`,
  `html-outer`, `markdown-full`, `markdown-reader`, `markdown-selector`. Backend
  is headless Firefox via WebDriver BiDi (only backend since chrest#47). Has
  `--timeout` (default 60s, deadline-backed context) and `--output <path>`
  (atomic tmpfile + rename; unlinks on failure). The CLI exits non-zero on any
  error. The markdown variants route through `*/markdown/` —
  `markdown-reader` runs go-readability on the DOM, `markdown-selector` takes a
  `--selector` CSS selector (first match); `--reader-engine` is reserved
  (`readability` default, `browser` NYI).
- `chrest capture-batch` - RFC 0001 capturer role (MVP, `split=false`). Reads
  a batch input JSON on stdin, runs captures sequentially, streams each
  artifact to a writer subprocess, and emits a result envelope on stdout.

### Other binaries (`go/cmd/`)

- `chrest-jcs` - Standalone JCS (RFC 8785) canonicalizer. Reads JSON on stdin,
  writes canonicalized bytes on stdout. Used for byte-stability cross-checks
  against the nebulous-side implementation.
- `chrest-server` - Native messaging host server binary.

### Capture Pipeline (`go/cmd/chrest/capture_batch.go`, `capture_serve.go`, `go/internal/*/capturebatch/`)

Implements both chrest-side transports of the **Capture Plugin Protocol**
([cutting-garden RFC 0002](https://github.com/amarbel-llc/cutting-garden/blob/master/docs/rfcs/0002-capture-plugin-protocol.md))
under the **web-archive binding** ([cutting-garden RFC 0003](https://github.com/amarbel-llc/cutting-garden/blob/master/docs/rfcs/0003-web-archive-binding.md)):
`capture-batch` (RFC 0002 subprocess transport, v1) and `capture-serve`
(RFC 0008 JSON-RPC transport, v2). The canonical RFCs live in the
cutting-garden repo; chrest is the reference implementation of the `web`
capture kind. See `docs/features/0001-web-page-capture.md` for the
chrest-side feature surface. Both transports landed (chrest#83,
chrest#98); RFC 0008 was accepted 2026-07-12, citing chrest's own
v1-vs-v2 conformance run as ratification evidence.

Fixtures shared with the nebulous orchestrator live under `~/eng/aim/fixtures/`
and are referenced by `explore-capture-batch` and `explore-jcs-fixture`.

Both transports emit the same receipt shape: one **receipt** ref per
capture, where each capture is a merkle tree of typed hyphence blobs
rooted at `cutting_garden-capture-receipt-web-v1`, assembled via the
shared `code.linenisgreat.com/cutting-garden/pkgs/capture_plugin.WriteReceipt`.
chrest supplies only the plugin-namespaced node bodies
(`jcs-chrest-capture-environment-v2`, `jcs-chrest-capture-outcome-v2` /
`-v2-preview`) and the payload bytes; the tree is byte-identical
regardless of transport — verified by
`go/cmd/chrest/capture_serve_conformance_test.go` — since receipt
assembly itself (`runOneWithWriter`/`buildReceipt`, below) has no
transport-specific branching.

- `capture-batch`: JSON-stdin/stdout, one `writer.cmd` subprocess per
  blob. Input/output schema `capture-plugin/v1` (`BatchSchema`).
- `capture-serve`: persistent JSON-RPC session over a self-created
  unixpacket rendezvous socket; blob bytes travel via `SCM_RIGHTS`
  fd-passing (`blob.begin`/`blob.finish`). Launched by an orchestrator
  with `CAPTURE_PLUGIN_COOKIE` set; never invoked directly. cutting-garden's
  web plugin always tries this first, falling back to `capture-batch` on
  clean bring-up failure.
- Receipt type `cutting_garden-capture-receipt-web-v1`; capturer
  identifier `chrest` (`CapturerName`).

The `cutting-garden` dependency is bridged via `go/gomod.nix`'s
`goFlakeInputs` (the same mechanism chrest uses for its other
amarbel-llc dependencies) — a real flake input fetched over SSH from the
forge. Its module path is `code.linenisgreat.com/cutting-garden` (the
`amarbel-llc/cutting-garden` GitHub mirror is archived/frozen at
v0.1.24). Transitive bridges (madder, hyphence, piggy, tap, crap, tommy)
inherit automatically at depth-1 through cutting-garden's own
`passthru.goFlakeInputs` — chrest does not re-declare them.
`GOPRIVATE=code.linenisgreat.com` in the devshell `shellHook` lets plain
`go build`/`go test`/`dagnabit` resolve the same dependency outside of
`nix build` too (there's no public go-get path via GOPROXY for a
self-hosted forge otherwise).

Files of note (`go/internal/echo/capturebatch/`):

- `types.go` - capture-plugin/v1 wire structs (`BatchInput`, `CaptureSpec`,
  `BatchOutput`, `ReceiptRef`) + `Resolve` (applies `defaults.plugin.browser` /
  `defaults.normalize`).
- `receipt.go` - assembles the receipt tree via `capture_plugin.WriteReceipt`;
  `cmdWriter` adapts the v1 `writer.cmd` subprocess to `capture_plugin.Writer`;
  `sizeTrackingWriter` wraps any `capture_plugin.Writer` (v1's `cmdWriter`
  or v2's Serve-supplied writer) to recover the root receipt's size, which
  `WriteReceipt` returns only the digest for.
- `mapping.go` - chrest-owned RFC 0003 node bodies: `environmentBody`
  (browser/extensions/isolation, isolation defaults to `fresh` —
  `command_line` deliberately excluded here, chrest#102) and
  `outcomeBody` (`process.command_line` whenever available, `http.*`
  when observed; lowercased headers, `timing_ms:{load}`, `resolved_ip`
  omitted; the returned type string is the full `-v2` schema iff `http.*`
  was observed, else `-v2-preview` — `process` presence never affects
  that choice), plus the payload type-segment mapper (hyphen→underscore).
- `serve.go` - `NewBatchHandler` adapts the same receipt-assembly path to
  cutting-garden's `capture_serve.BatchFunc` signature (RFC 0008).
- `runner.go` - `runOneWithWriter` drives one capture end to end
  (open session, navigate, capture, optional normalize, `buildReceipt`)
  against any `sizeTrackingWriter`; shared verbatim by both transports.
- `mhtml.go`, `pdf.go`, `png.go`, `normalize.go` - format-specific normalizers;
  applied when `normalize` is requested and the format has one, stripping
  non-deterministic bits into the outcome subtree.
- `writer.go` - streams each node blob through the orchestrator-supplied
  `writer.cmd` subprocess (`WriteThrough`) — v1 only.
- `jcs.go` + `jcs_test.go` - homegrown JCS, retained for the standalone
  `chrest-jcs` byte-stability tool; the receipt path uses `capture_plugin.JCS`.

`go/cmd/chrest/capture_serve.go` is the v2 bring-up sequence:
`CookieFromEnv` → `ListenRendezvous` → print `AnnounceLine` on stdout →
`AcceptUnix` → `Serve`, with a lifecycle wrapper for stdin-EOF/SIGTERM —
including closing the rendezvous listener on lifecycle-context
cancellation, so a pending `AcceptUnix` doesn't block forever if the
orchestrator never dials in (a real gap this implementation and
cutting-garden's own reference plugin found independently).

Known open issues:

- **chrest#27** — PDF byte-stability. Pdfcpu has map-iteration-dependent
  object numbering + stream placement; two normalizePDF calls on the same
  input can produce outputs differing in length by 1 byte with the first
  diff near offset 309. The `/Info` + `/ID` scrub landed in `pdf.go` but
  full determinism still requires pdfcpu-side work. `explore-pdf-inspect-info`
  recipe decompresses FlateDecode streams to inspect `/Info` placement.

### MCP Server (`chrest mcp`)

Exposes browser management as MCP tools and resources over stdio (JSON-RPC 2.0).

**Tools** — all browser tools (list-windows, create-tab, close-tab, etc.) plus:

- `capture` — fetch or capture a web page via headless Firefox. Five formats:
  `markdown` (default), `text`, `html`, `pdf`, `screenshot-png`. Hand-registered
  directly against the MCP tool registry in `go/cmd/chrest/main.go` (not
  wired through dewey's `Command`/`RegisterMCPToolsV1`, which can't carry
  binary content blocks) — the handler unmarshals args into
  `tools.CaptureParams` (the same struct the `chrest capture` CLI uses) plus a
  `refresh` bool.
  - `markdown`/`text`/`html`: all three base formats are always rendered;
    non-selected formats come back as `resource_link` URIs (`capture://<url>#<fragment>`)
    readable via `read-resource`. A TOC of `#id` anchors is included inline
    unless a `selector` is supplied and matches, in which case the TOC becomes
    a resource_link instead. Results are cached per URL for the session
    lifetime (`fetchCache`); pass `refresh: true` to force a re-fetch.
  - `pdf`/`screenshot-png`: reuses `tools.CaptureParams.Validate()` and
    `tools.MultiExtract` (the same machinery as `chrest capture`) to render a
    **fresh, uncached** capture every call, then returns the bytes inline as
    base64 — `protocol.ImageContentV1` for screenshot-png, an
    `protocol.EmbeddedBlobResourceContent` blob (`application/pdf`) for pdf.
    Accepts the same pdf/screenshot flags as the CLI (`landscape`,
    `no-headers`, `background`, `paper-width`, `paper-height`, `margin-*`,
    `full-page`), validated the same way. Not part of the `capture://`
    resource-link/cache scheme — no re-fetch via `read-resource`.
- `read-resource` — bridge tool so subagents can access MCP resources via tools/call

**Resources:**

- `chrest://items` — paginated index (total count, page URIs)
- `chrest://items/{page}` — 100 items per page (tabs, bookmarks, history)
- Items are cached for 30s to handle concurrent reads

**Annotations:** read-only tools have `readOnlyHint`, destructive tools (close-\*, state-restore, items-put) have `destructiveHint`. Validated by `just test-mcp`.

### Runtime configuration

`CHREST_WEB_FETCH_DISPATCH` controls how the `capture` MCP tool's
markdown/text/html formats fetch URLs (pdf/screenshot-png always go through
`tools.MultiExtract` directly and are unaffected by this variable):

- `bidi-intercept` (default) — classify via WebDriver BiDi response interception;
  HTML routes through Firefox/MultiExtract, raw text routes through
  `*/rawfetch/`, binary and non-2xx responses return structured errors. See
  `docs/plans/2026-04-29-web-fetch-content-type-dispatch-design.md`.
- `firefox-only` — preserve the pre-dispatch behavior (every URL through
  Firefox/MultiExtract, no classification). Rollback target during the
  dual-architecture period.

### Extension REST Routes (`extension/src/routes.js`)

- `/` - Browser info
- `/windows`, `/windows/#WINDOW_ID` - Window CRUD
- `/tabs`, `/tabs/#TAB_ID` - Tab CRUD
- `/state` - Save/restore/clear browser state
- `/items` - Unified tabs, bookmarks, history
- `/bookmarks`, `/history` - Read-only access
- `/extensions` - List extensions
- `/runtime/reload` - Reload extension

## Development Environment

Uses nix flakes with direnv. The root `flake.nix` provides a dev shell with Go and JS tooling via `devenv-go` and `devenv-js` from `github:friedenberg/eng?dir=pkgs/alfa`.

## Design Docs and Tests

- `docs/plans/` holds dated design docs and implementation plans (e.g.
  `2026-04-14-cdp-capture-commands-design.md`). Check here first when picking
  up in-flight feature work — the `design` + implementation file pair together
  is the contract for a given chunk of work.
- `zz-tests_bats/` - BATS integration tests that exercise `chrest` end-to-end
  against real unix sockets (`--allow-unix-sockets`). Suites:
  `capture_batch.bats`, `capture_firefox.bats`, `mcp.bats`. Run via
  `just test-mcp-bats`.

## Usage Example

```bash
# Get all windows and tabs
http --ignore-stdin --offline localhost/windows | chrest client | jq

# Create new window with URL
http --ignore-stdin --offline localhost/windows url[]=https://example.com | chrest client | jq

# Close a window
http --ignore-stdin --offline DELETE localhost/windows/1234 | chrest client
```
