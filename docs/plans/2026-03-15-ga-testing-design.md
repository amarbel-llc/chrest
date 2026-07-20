# Design: End-to-end GA testing, fixes, and VHS demo scripts

**Date:** 2026-03-15
**Branch:** fond-linden

## Summary

Test the full install → setup → CLI usage → MCP flow for Chrome and Firefox.
Fix issues found. Produce two VHS scripts (one per browser) showing setup and
CLI usage for store listings.

## Motivation

Chrest is approaching GA with Chrome Web Store submission pending. The
installation and usage flow has rough edges from a partial refactoring
(go-mcp migration, multi-browser support). Before release:

- `chrest init` is hidden and lacks user-facing output
- Multi-browser APIs have inconsistencies
- No automated demo capturing setup and usage
- README is stale (references old `go build` + `chrest install` flow)

## Approach

Interleaved audit-fix-test per layer. Each layer is manually tested against
real browsers before proceeding to the next.

## Layer 1: `chrest init` refactor

### Current state

Hidden command. Takes `--browser`, `--chrome`, `--firefox` as strings. No TTY
detection, no interactive fallback, no user-facing output. Installs native host
for both browsers regardless of input.

### Target UX

```
# Explicit browser
$ chrest init --browser chrome
TAP version 14
ok 1 - Wrote config to ~/.config/chrest/config.json
ok 2 - Symlinked chrest-server to ~/.local/bin/chrest-server
ok 3 - Installed native messaging host for Chrome (extension: faeaeoifckcedjniagocclagdbbkifgo)
1..3

# Interactive (TTY, no --browser)
$ chrest init
? Default browser: [chrome/firefox] > chrome
TAP version 14
ok 1 - ...

# Non-TTY, no --browser
$ echo | chrest init
Error: --browser is required when not interactive
```

### Changes

- Unhide `init` command
- Add dependency: `github.com/amarbel-llc/bob/packages/tap-dancer/go` for
  TAP-14 step output
- Add dependency: `code.linenisgreat.com/purse-first/libs/go-mcp/command/huh`
  for interactive prompts
- Switch `main.go` CLI path from `StubPrompter{}` to `huh.Prompter{}`
- `--browser` required; if omitted, fall back to `prompter.Select()`;
  if prompter fails (non-TTY / MCP), hard error
- `--chrome` / `--firefox` remain optional (defaults from `GetDefaultId()`)
- Install native host only for the selected browser
- Report each step via tap-dancer `Writer` (ok/not ok)
- Update README installation section

## Layer 2: CLI commands audit & fixes

Audit the full CLI command surface for issues:

- **Multi-browser proxy** — does `BrowserProxy` respect default browser from
  config? Graceful handling of no sockets found?
- **`client` command** — works for both browsers? Error messages when server
  not running?
- **`reload-extension`** — Chrome and Firefox parity?
- **Tool commands** (list-windows, create-tab, etc.) — consistent parameter
  naming, error handling when extension unreachable
- **Stale sockets** — leftover `.sock` files after browser close causing
  confusing errors

Fix issues found. Manual testing against real Chrome and Firefox between fixes.

## Layer 3: MCP audit & VHS scripts

### MCP audit

- `chrest mcp` starts cleanly, tools register correctly
- All tools work via MCP (same proxy path as CLI)
- `install-mcp` produces correct Claude Code config
- Error behavior when no browser connected

### VHS scripts

Two scripts: `demo-chrome.tape`, `demo-firefox.tape`.

Each script demonstrates:

1. `chrest init --browser {chrome,firefox}` — TAP output showing success
2. CLI usage — list windows, create tab, list tabs, close tab

VHS cannot drive browser GUI, so extension loading is narrated. MCP demo
out of scope for VHS.

## Not in scope

- Browser auto-detection in `init`
- Homebrew package
- Chrome Web Store submission
- MCP demo in VHS scripts
