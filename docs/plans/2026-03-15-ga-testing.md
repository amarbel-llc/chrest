# GA Testing Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task.

**Goal:** Fix the chrest setup/usage flow for Chrome and Firefox, then produce VHS demo scripts.

**Architecture:** Three layers executed sequentially — `chrest init` refactor, CLI audit/fix, MCP audit + VHS scripts. Each layer is manually tested against real browsers before proceeding.

**Tech Stack:** Go (go-mcp command framework, tap-dancer TAP-14 writer, huh prompter), VHS tape files

**Rollback:** N/A — purely additive improvements to existing commands.

---

### Task 1: Add tap-dancer and huh dependencies

**Promotion criteria:** N/A

**Files:**

- Modify: `go/go.mod`
- Modify: `go/go.sum`

**Step 1: Add dependencies**

Run:

```bash
cd /home/sasha/eng/repos/chrest/.worktrees/fond-linden/go
go get github.com/amarbel-llc/bob/packages/tap-dancer/go@latest
go get github.com/amarbel-llc/purse-first/libs/go-mcp/command/huh@latest
go mod tidy
```

**Step 2: Verify build**

Run: `cd /home/sasha/eng/repos/chrest/.worktrees/fond-linden/go && go build ./...`
Expected: clean build

**Step 3: Commit**

```
feat: add tap-dancer and huh prompter dependencies
```

---

### Task 2: Switch CLI path to huh.Prompter

**Promotion criteria:** N/A

**Files:**

- Modify: `go/cmd/chrest/main.go:9-11` (imports)
- Modify: `go/cmd/chrest/main.go:81` (StubPrompter → huh.Prompter)

**Step 1: Update imports**

Add import:

```go
huhprompter "github.com/amarbel-llc/purse-first/libs/go-mcp/command/huh"
```

**Step 2: Replace StubPrompter with huh.Prompter**

Change line 81 from:

```go
if err = app.RunCLI(ctx, os.Args[1:], command.StubPrompter{}); err != nil {
```

to:

```go
if err = app.RunCLI(ctx, os.Args[1:], huhprompter.Prompter{}); err != nil {
```

**Step 3: Verify build**

Run: `cd /home/sasha/eng/repos/chrest/.worktrees/fond-linden/go && go build ./cmd/chrest/`
Expected: clean build

**Step 4: Commit**

```
feat: use interactive huh prompter for CLI mode
```

---

### Task 3: Refactor `chrest init` — interactive browser, TAP output, single-browser install

This is the core refactor. The init command switches from `RunCLI` to `Run`
(to receive the Prompter), adds TAP-14 output, and installs only the selected
browser's native messaging host.

**Promotion criteria:** N/A

**Files:**

- Modify: `go/cmd/chrest/init.go`

**Step 1: Rewrite init.go**

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"code.linenisgreat.com/chrest/go/src/alfa/browser"
	"code.linenisgreat.com/chrest/go/src/alfa/symlink"
	"code.linenisgreat.com/chrest/go/src/bravo/config"
	"code.linenisgreat.com/chrest/go/src/charlie/install"
	"code.linenisgreat.com/dodder/go/lib/bravo/errors"
	tap "github.com/amarbel-llc/bob/packages/tap-dancer/go"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/command"
)

func registerInitCommand(app *command.App) {
	app.AddCommand(&command.Command{
		Name: "init",
		Description: command.Description{
			Short: "Initialize configuration and install native messaging host",
		},
		Params: []command.Param{
			{Name: "browser", Type: command.String, Description: "Default browser (chrome or firefox)"},
			{Name: "extension-id", Type: command.String, Description: "Custom extension ID (uses default if omitted)"},
		},
		Run: func(ctx context.Context, args json.RawMessage, p command.Prompter) (*command.Result, error) {
			return nil, cmdInit(ctx, args, p)
		},
	})
}

func cmdInit(ctx context.Context, args json.RawMessage, p command.Prompter) (err error) {
	var params struct {
		Browser     string `json:"browser"`
		ExtensionId string `json:"extension-id"`
	}

	if err = json.Unmarshal(args, &params); err != nil {
		err = errors.Wrap(err)
		return
	}

	// If browser not provided, prompt interactively
	if params.Browser == "" {
		var idx int
		options := []string{"chrome", "firefox"}

		if idx, err = p.Select("Default browser", options); err != nil {
			err = errors.Errorf("--browser is required when not interactive")
			return
		}

		params.Browser = options[idx]
	}

	w := tap.NewColorWriter(os.Stderr, true)
	defer w.Plan()

	var initConfig config.Config

	if initConfig, err = config.Default(); err != nil {
		w.NotOk("Read default config", map[string]string{"error": err.Error()})
		err = errors.Wrap(err)
		return
	}

	if err = initConfig.DefaultBrowser.Set(params.Browser); err != nil {
		w.NotOk(
			fmt.Sprintf("Set default browser to %s", params.Browser),
			map[string]string{"error": err.Error()},
		)
		err = errors.Wrap(err)
		return
	}

	errCtx := errors.MakeContext(ctx)

	if err = initConfig.Write(errCtx); err != nil {
		w.NotOk("Write config", map[string]string{"error": err.Error()})
		err = errors.Wrap(err)
		return
	}

	w.Ok(fmt.Sprintf("Wrote config to %s", initConfig.Directory()))

	var exe string

	if exe, err = os.Executable(); err != nil {
		w.NotOk("Find executable path", map[string]string{"error": err.Error()})
		err = errors.Wrap(err)
		return
	}

	serverPath := initConfig.ServerPath()

	if err = symlink.Symlink(exe, serverPath); err != nil {
		w.NotOk(
			fmt.Sprintf("Symlink chrest-server to %s", serverPath),
			map[string]string{"error": err.Error()},
		)
		err = errors.Wrap(err)
		return
	}

	w.Ok(fmt.Sprintf("Symlinked chrest-server to %s", serverPath))

	var idSet install.IdSet

	if err = idSet.Browser.Set(params.Browser); err != nil {
		err = errors.Wrap(err)
		return
	}

	if params.ExtensionId != "" {
		if err = idSet.Set(params.ExtensionId); err != nil {
			err = errors.Wrap(err)
			return
		}
	}

	extensionId := params.ExtensionId
	if extensionId == "" {
		extensionId = idSet.GetDefaultId()
	}

	if _, _, err = idSet.Install(initConfig); err != nil {
		w.NotOk(
			fmt.Sprintf("Install native messaging host for %s", params.Browser),
			map[string]string{"error": err.Error()},
		)
		err = errors.Wrap(err)
		return
	}

	w.Ok(fmt.Sprintf(
		"Installed native messaging host for %s (extension: %s)",
		params.Browser,
		extensionId,
	))

	return
}
```

**Step 2: Verify build**

Run: `cd /home/sasha/eng/repos/chrest/.worktrees/fond-linden/go && go build ./cmd/chrest/`
Expected: clean build

**Step 3: Test with explicit browser**

Run: `cd /home/sasha/eng/repos/chrest/.worktrees/fond-linden/go && go run ./cmd/chrest/ init --browser chrome`
Expected: TAP output with 3 ok lines

**Step 4: Test interactive fallback (TTY)**

Run: `cd /home/sasha/eng/repos/chrest/.worktrees/fond-linden/go && go run ./cmd/chrest/ init`
Expected: huh select prompt appears

**Step 5: Commit**

```
feat: refactor init with interactive browser prompt and TAP output
```

---

### Task 4: Update README installation section

**Promotion criteria:** N/A

**Files:**

- Modify: `README.md`

**Step 1: Replace installation section**

Replace everything from `# Installation` to the end of the installation steps
with updated instructions reflecting the new flow:

1. Install CLI (`go install` or nix)
2. Install browser extension (load unpacked for Chrome, temporary for Firefox)
3. Run `chrest init --browser chrome` (or firefox)
4. Reload extension
5. Verify: `chrest list-windows`

Also update the header to mention Firefox support (currently says "Chrome
extension" only).

**Step 2: Commit**

```
docs: update README with new init flow and Firefox support
```

---

### Task 5: Manual testing checkpoint — Layer 1

**User action required.** Test the following against real Chrome and Firefox:

1. `chrest init --browser chrome` — verify TAP output, config written, symlink created, native host manifest correct
2. `chrest init --browser firefox` — same
3. `chrest init` (interactive) — verify prompt appears
4. Reload extension in browser
5. `chrest list-windows` — verify basic connectivity

Report issues found. We fix before proceeding to Layer 2.

---

### Task 6: Audit and fix CLI commands

**Files (likely):**

- Modify: `go/cmd/chrest/client.go`
- Modify: `go/cmd/chrest/reload_extension.go`
- Modify: `go/src/delta/proxy/main.go`
- Modify: `go/src/delta/tools/*.go`
- Modify: `go/src/bravo/config/main.go`

**Step 1: Audit multi-browser proxy**

Read `delta/proxy/main.go` — check that `BrowserProxy` uses the default
browser from config when no specific browser is requested. Check behavior
when no sockets exist.

**Step 2: Audit client command**

Read `cmd/chrest/client.go` — check `--browser` flag handling, error messages
when server not running, stale socket cleanup.

**Step 3: Audit reload-extension**

Check Firefox compatibility.

**Step 4: Audit all tool commands**

Check parameter consistency, error handling when extension is unreachable.

**Step 5: Fix issues found**

Apply fixes. Each fix is a separate commit.

**Step 6: Run tests**

Run: `cd /home/sasha/eng/repos/chrest/.worktrees/fond-linden/go && just tests-go`
Expected: all pass

---

### Task 7: Manual testing checkpoint — Layer 2

**User action required.** Test CLI commands against both browsers:

1. `chrest list-windows`
2. `chrest create-tab --url https://example.com`
3. `chrest list-tabs`
4. `chrest close-tab --id <tab-id>`
5. `chrest client` with httpie pipe
6. `chrest reload-extension`

Report issues. Fix before proceeding.

---

### Task 8: Audit and fix MCP server

**Files (likely):**

- Modify: `go/cmd/chrest/main.go` (MCP path)
- Modify: `go/cmd/chrest/install_mcp.go`
- Modify: `go/src/delta/tools/*.go`

**Step 1: Test MCP server startup**

Run: `echo '{"jsonrpc":"2.0","method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"0.1"}},"id":1}' | go run ./cmd/chrest/ mcp`
Expected: valid JSON-RPC response

**Step 2: Test install-mcp**

Run: `go run ./cmd/chrest/ install-mcp`
Verify config written correctly.

**Step 3: Fix issues found**

Each fix is a separate commit.

---

### Task 9: Write VHS scripts

**Promotion criteria:** N/A

**Files:**

- Create: `demo-chrome.tape`
- Create: `demo-firefox.tape`

**Step 1: Write Chrome VHS script**

```tape
# demo-chrome.tape
Output demo-chrome.gif

Set FontSize 16
Set Width 1200
Set Height 600
Set Theme "Catppuccin Mocha"

Type "# Install chrest CLI"
Enter
Sleep 500ms

Type "go install code.linenisgreat.com/chrest/go/cmd/chrest@latest"
Enter
Sleep 2s

Type "# Initialize for Chrome"
Enter
Sleep 500ms

Type "chrest init --browser chrome"
Enter
Sleep 2s

Type "# List browser windows"
Enter
Sleep 500ms

Type "chrest list-windows"
Enter
Sleep 3s

Type "# Open a new tab"
Enter
Sleep 500ms

Type "chrest create-tab --url https://example.com"
Enter
Sleep 3s

Type "# List all tabs"
Enter
Sleep 500ms

Type "chrest list-tabs"
Enter
Sleep 3s
```

**Step 2: Write Firefox VHS script**

Same structure, `--browser firefox`, output `demo-firefox.gif`.

**Step 3: Test VHS scripts**

Run: `vhs demo-chrome.tape`
Verify GIF is produced and looks correct.

**Step 4: Commit**

```
feat: add VHS demo scripts for Chrome and Firefox
```

---

### Task 10: Final manual validation

**User action required.** Review:

1. Run both VHS scripts, inspect GIFs
2. Full flow: fresh init → CLI usage for both browsers
3. MCP server via Claude Code
4. README accuracy
