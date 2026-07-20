# Design: Migrate chrest MCP from go-sdk to go-mcp

**Date:** 2026-03-07
**Branch:** free-yew

## Summary

Replace `github.com/modelcontextprotocol/go-sdk` with
`code.linenisgreat.com/purse-first/libs/go-mcp`. Unify all CLI commands and
MCP tools under `command.App`. Drop the custom scope system in favor of V1 tool
annotations. Replace the `install-mcp` flake app with `generate-plugin`.

## Motivation

- CLI commands (`items-get`, `items-put`) and MCP tools (`get_browser_items`,
  `manage_browser_items`) are parallel implementations of the same operations.
  `command.App` eliminates this duplication.
- V1 protocol support (tool annotations, streamable HTTP transport) for free.
- purse-first plugin integration replaces the manual `install-mcp` flake app.
- Alignment with grit, get-hubbed, mgp, lux, tap-dancer — all use go-mcp.

## Architecture

### Before

```
main.go (switch/case dispatch) → CmdClient, CmdItemsGet, CmdMcp, ...
delta/mcp/ (go-sdk server)    → separate tool registration + handlers
```

### After

```
main.go → app := command.NewApp("chrest", ...)
          tools.RegisterAll(app)     // all browser-facing commands
          registerCLICommands(app)   // client, reload-extension, init, generate-plugin

          "mcp" subcommand:  app.RegisterMCPToolsV1(registry) → server.Run()
          everything else:   app.RunCLI(ctx, args, prompter)
```

The `delta/mcp/` package is replaced by `delta/tools/` which registers
`command.Command` definitions on the app. Server setup lives in the `mcp`
subcommand handler in `cmd/chrest/`.

## Command Mapping

| Current CLI        | Current MCP Tool        | New Command        | Annotations      |
| ------------------ | ----------------------- | ------------------ | ---------------- |
| —                  | `browser_info`          | `browser-info`     | ReadOnly         |
| —                  | `list_extensions`       | `list-extensions`  | ReadOnly         |
| —                  | `list_windows`          | `list-windows`     | ReadOnly         |
| —                  | `get_window`            | `get-window`       | ReadOnly         |
| —                  | `create_window`         | `create-window`    |                  |
| —                  | `update_window`         | `update-window`    |                  |
| —                  | `close_window`          | `close-window`     | Destructive      |
| —                  | `list_tabs`             | `list-tabs`        | ReadOnly         |
| —                  | `get_tab`               | `get-tab`          | ReadOnly         |
| —                  | `create_tab`            | `create-tab`       |                  |
| —                  | `update_tab`            | `update-tab`       |                  |
| —                  | `close_tab`             | `close-tab`        | Destructive      |
| `items-get`        | `get_browser_items`     | `items-get`        | ReadOnly         |
| `items-put`        | `manage_browser_items`  | `items-put`        |                  |
| —                  | `get_browser_state`     | `state-get`        | ReadOnly         |
| —                  | `restore_browser_state` | `state-restore`    |                  |
| `client`           | —                       | `client`           | CLI-only         |
| `reload-extension` | —                       | `reload-extension` | CLI-only         |
| `init`             | —                       | `init`             | CLI-only, Hidden |
| —                  | —                       | `generate-plugin`  | CLI-only, Hidden |

CLI-only commands have `RunCLI` but no `Run`, so `RegisterMCPToolsV1` skips
them. The `mcp` subcommand is special-cased as a mode switch, not a command.

## Handler Pattern

Commands use `command.Run` with manual JSON unmarshal:

```go
&command.Command{
    Name:        "get-window",
    Description: command.Description{Short: "Get details of a specific window by ID"},
    Annotations: &protocol.ToolAnnotations{ReadOnlyHint: protocol.BoolPtr(true)},
    Params: []command.Param{
        {Name: "window_id", Type: command.String, Required: true},
    },
    Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
        var p struct{ WindowID string `json:"window_id"` }
        json.Unmarshal(args, &p)
        result, err := proxy.requestAllBrowsers(ctx, "GET", "/windows/"+p.WindowID, nil)
        if err != nil {
            return nil, err
        }
        return command.TextResult(result), nil
    },
}
```

The `requestAllBrowsers` / `requestOneBrowser` helpers move from methods on
`Server` to methods on a shared proxy object captured by command closures.

## Scope System Removal

The current `ScopeConfig` (tabs:read, windows:write, etc.) with conditional tool
registration is removed. V1 `ToolAnnotations` replace it:

- `ReadOnlyHint: true` — read-only tools (list, get, browser-info)
- `DestructiveHint: true` — destructive tools (close-window, close-tab)
- Neither — mutation tools that aren't destructive (create, update)

All tools are always registered. The MCP client decides how to handle
annotations.

## Installation

The `install-mcp` flake app is removed. A hidden `generate-plugin` command
generates `plugin.json` for purse-first integration. The nix build calls
`generate-plugin` in postInstall to produce artifacts at
`$out/share/purse-first/chrest/`.

## Transport

- **stdio** (default) — `transport.NewStdio(os.Stdin, os.Stdout)`
- **http** — `transport.NewStreamableHTTP(addr)` (replaces the deprecated SSE
  transport from go-sdk)

## Rollback

No dual-architecture period — go-sdk and go-mcp are alternative protocol
implementations. Work is on the `free-yew` worktree; master is untouched.
Rollback is: don't merge, or `git revert` the merge commit.

**Promotion criteria:** `just build` + `just build-nix` succeed. Manual
verification against a running browser confirms MCP tools and CLI commands work.
