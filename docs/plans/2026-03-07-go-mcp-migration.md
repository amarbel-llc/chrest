# go-mcp Migration Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to
> implement this plan task-by-task.

**Goal:** Replace go-sdk with go-mcp, unify CLI and MCP under command.App, add
purse-first plugin support.

**Architecture:** Single `command.App` owns all commands. MCP mode registers
them as V1 tools. CLI mode dispatches via `app.RunCLI`. Browser proxy is a
shared object captured by command closures.

**Tech Stack:** `github.com/amarbel-llc/purse-first/libs/go-mcp` (command,
server, transport, protocol packages)

**Rollback:** Work is on `free-yew` worktree. Don't merge, or `git revert`.

**Design doc:** `docs/plans/2026-03-07-go-mcp-migration-design.md`

---

### Task 1: Swap go-sdk for go-mcp in go.mod

**Files:**

- Modify: `go/go.mod`
- Modify: `go/go.sum`

**Step 1: Remove go-sdk dependency**

```bash
cd /home/sasha/eng/repos/chrest/.worktrees/free-yew/go
go get -u github.com/modelcontextprotocol/go-sdk@none
```

**Step 2: Add go-mcp dependency**

```bash
go get github.com/amarbel-llc/purse-first/libs/go-mcp@latest
```

**Step 3: Tidy**

```bash
go mod tidy
```

Note: This will break the build until later tasks rewrite the imports. That's
expected — we commit the dependency swap first, then fix the code.

**Step 4: Commit**

```
chore: swap go-sdk for go-mcp in go.mod
```

---

### Task 2: Create browser proxy object

Extract `requestAllBrowsers` and `requestOneBrowser` from `delta/mcp/handlers.go`
into a standalone proxy type that doesn't depend on go-sdk. Commands will close
over this object.

**Files:**

- Create: `go/src/delta/proxy/main.go`

**Step 1: Create the proxy package**

```go
package proxy

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"sync"

	"code.linenisgreat.com/chrest/go/src/bravo/config"
	"code.linenisgreat.com/dodder/go/lib/bravo/errors"
)

type Proxy struct {
	Config  config.Config
	Sockets []string // if non-empty, only query these sockets
}

func (p *Proxy) GetSockets() ([]string, error) {
	if len(p.Sockets) > 0 {
		return p.Sockets, nil
	}
	return p.Config.GetAllSockets()
}

func (p *Proxy) RequestAllBrowsers(
	ctx context.Context,
	method string,
	path string,
	body any,
) (string, error) {
	socks, err := p.GetSockets()
	if err != nil {
		return "", errors.Wrap(err)
	}

	if len(socks) == 0 {
		return "[]", nil
	}

	wg := errors.MakeWaitGroupParallel()
	var l sync.Mutex
	var allResults []any

	for _, sock := range socks {
		wg.Do(func() (err error) {
			result, err := p.requestOneBrowser(ctx, sock, method, path, body)
			if err != nil {
				return nil
			}

			l.Lock()
			defer l.Unlock()

			if arr, ok := result.([]any); ok {
				allResults = append(allResults, arr...)
			} else if result != nil {
				allResults = append(allResults, result)
			}

			return nil
		})
	}

	if err = wg.GetError(); err != nil {
		return "", errors.Wrap(err)
	}

	jsonBytes, err := json.MarshalIndent(allResults, "", "  ")
	if err != nil {
		return "", errors.Wrap(err)
	}

	return string(jsonBytes), nil
}

func (p *Proxy) requestOneBrowser(
	ctx context.Context,
	sock string,
	method string,
	path string,
	body any,
) (any, error) {
	var bodyReader *json.Encoder

	pr, pw := net.Pipe()

	if body != nil {
		bodyReader = json.NewEncoder(pw)
		_ = bodyReader // suppress unused
		go func() {
			json.NewEncoder(pw).Encode(body)
			pw.Close()
		}()
	} else {
		pw.Close()
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, path, pr)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	if body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}

	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", sock)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	defer conn.Close()

	if err = httpReq.Write(conn); err != nil {
		return nil, errors.Wrap(err)
	}

	resp, err := http.ReadResponse(bufio.NewReader(conn), httpReq)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 204 {
		return map[string]string{"status": "success"}, nil
	}

	var result any
	if err = json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, errors.Wrap(err)
	}

	return result, nil
}
```

**Step 2: Verify it compiles**

```bash
cd /home/sasha/eng/repos/chrest/.worktrees/free-yew/go
go build ./src/delta/proxy/
```

**Step 3: Commit**

```
refactor: extract browser proxy from MCP server into delta/proxy
```

---

### Task 3: Create command registration with go-mcp

Register all browser-facing commands on a `command.App`. This is the core of the
migration — every MCP tool and shared CLI command becomes a `command.Command`.

**Files:**

- Create: `go/src/delta/tools/main.go`
- Create: `go/src/delta/tools/browser.go` (browser-info, list-extensions)
- Create: `go/src/delta/tools/windows.go` (list/get/create/update/close windows)
- Create: `go/src/delta/tools/tabs.go` (list/get/create/update/close tabs)
- Create: `go/src/delta/tools/items.go` (items-get, items-put)
- Create: `go/src/delta/tools/state.go` (state-get, state-restore)

**Step 1: Create `main.go` with `RegisterAll`**

```go
package tools

import (
	"github.com/amarbel-llc/purse-first/libs/go-mcp/command"

	"code.linenisgreat.com/chrest/go/src/delta/proxy"
)

func RegisterAll(app *command.App, p *proxy.Proxy) {
	registerBrowserTools(app, p)
	registerWindowTools(app, p)
	registerTabTools(app, p)
	registerItemTools(app, p)
	registerStateTools(app, p)
}
```

**Step 2: Create `browser.go`**

```go
package tools

import (
	"context"
	"encoding/json"

	"github.com/amarbel-llc/purse-first/libs/go-mcp/command"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/protocol"

	"code.linenisgreat.com/chrest/go/src/delta/proxy"
)

func registerBrowserTools(app *command.App, p *proxy.Proxy) {
	app.AddCommand(&command.Command{
		Name:        "browser-info",
		Description: command.Description{
			Short: "Get browser information from all connected browsers",
		},
		Annotations: &protocol.ToolAnnotations{
			ReadOnlyHint: protocol.BoolPtr(true),
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			result, err := p.RequestAllBrowsers(ctx, "GET", "/", nil)
			if err != nil {
				return nil, err
			}
			return command.TextResult(result), nil
		},
	})

	app.AddCommand(&command.Command{
		Name:        "list-extensions",
		Description: command.Description{
			Short: "List all installed browser extensions",
		},
		Annotations: &protocol.ToolAnnotations{
			ReadOnlyHint: protocol.BoolPtr(true),
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			result, err := p.RequestAllBrowsers(ctx, "GET", "/extensions", nil)
			if err != nil {
				return nil, err
			}
			return command.TextResult(result), nil
		},
	})
}
```

**Step 3: Create `windows.go`**

```go
package tools

import (
	"context"
	"encoding/json"

	"github.com/amarbel-llc/purse-first/libs/go-mcp/command"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/protocol"

	"code.linenisgreat.com/chrest/go/src/delta/proxy"
)

func registerWindowTools(app *command.App, p *proxy.Proxy) {
	app.AddCommand(&command.Command{
		Name:        "list-windows",
		Description: command.Description{
			Short: "List all browser windows with their tabs",
		},
		Annotations: &protocol.ToolAnnotations{
			ReadOnlyHint: protocol.BoolPtr(true),
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			result, err := p.RequestAllBrowsers(ctx, "GET", "/windows", nil)
			if err != nil {
				return nil, err
			}
			return command.TextResult(result), nil
		},
	})

	app.AddCommand(&command.Command{
		Name:        "get-window",
		Description: command.Description{
			Short: "Get details of a specific window by ID",
		},
		Annotations: &protocol.ToolAnnotations{
			ReadOnlyHint: protocol.BoolPtr(true),
		},
		Params: []command.Param{
			{Name: "window_id", Type: command.String, Required: true, Description: "Window ID"},
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var p2 struct{ WindowID string `json:"window_id"` }
			if err := json.Unmarshal(args, &p2); err != nil {
				return command.TextErrorResult(err.Error()), nil
			}
			result, err := p.RequestAllBrowsers(ctx, "GET", "/windows/"+p2.WindowID, nil)
			if err != nil {
				return nil, err
			}
			return command.TextResult(result), nil
		},
	})

	app.AddCommand(&command.Command{
		Name:        "create-window",
		Description: command.Description{
			Short: "Create a new browser window with optional URLs",
		},
		Params: []command.Param{
			{Name: "urls", Type: command.Array, Description: "URLs to open in the new window"},
			{Name: "focused", Type: command.Bool, Description: "Whether to focus the new window"},
			{Name: "incognito", Type: command.Bool, Description: "Whether to open in incognito mode"},
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var p2 struct {
				URLs      []string `json:"urls,omitempty"`
				Focused   bool     `json:"focused,omitempty"`
				Incognito bool     `json:"incognito,omitempty"`
			}
			if err := json.Unmarshal(args, &p2); err != nil {
				return command.TextErrorResult(err.Error()), nil
			}
			body := map[string]any{}
			if len(p2.URLs) > 0 {
				body["url"] = p2.URLs[0]
			}
			if p2.Focused {
				body["focused"] = true
			}
			if p2.Incognito {
				body["incognito"] = true
			}
			result, err := p.RequestAllBrowsers(ctx, "POST", "/windows", body)
			if err != nil {
				return nil, err
			}
			return command.TextResult(result), nil
		},
	})

	app.AddCommand(&command.Command{
		Name:        "update-window",
		Description: command.Description{
			Short: "Update properties of a specific window",
		},
		Params: []command.Param{
			{Name: "window_id", Type: command.String, Required: true, Description: "Window ID"},
			{Name: "focused", Type: command.Bool, Description: "Whether to focus the window"},
			{Name: "state", Type: command.String, Description: "Window state (normal, minimized, maximized, fullscreen)"},
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var p2 struct {
				WindowID string `json:"window_id"`
				Focused  bool   `json:"focused,omitempty"`
				State    string `json:"state,omitempty"`
			}
			if err := json.Unmarshal(args, &p2); err != nil {
				return command.TextErrorResult(err.Error()), nil
			}
			body := map[string]any{}
			if p2.Focused {
				body["focused"] = true
			}
			if p2.State != "" {
				body["state"] = p2.State
			}
			result, err := p.RequestAllBrowsers(ctx, "PUT", "/windows/"+p2.WindowID, body)
			if err != nil {
				return nil, err
			}
			return command.TextResult(result), nil
		},
	})

	app.AddCommand(&command.Command{
		Name:        "close-window",
		Description: command.Description{
			Short: "Close a browser window by ID",
		},
		Annotations: &protocol.ToolAnnotations{
			DestructiveHint: protocol.BoolPtr(true),
		},
		Params: []command.Param{
			{Name: "window_id", Type: command.String, Required: true, Description: "Window ID"},
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var p2 struct{ WindowID string `json:"window_id"` }
			if err := json.Unmarshal(args, &p2); err != nil {
				return command.TextErrorResult(err.Error()), nil
			}
			result, err := p.RequestAllBrowsers(ctx, "DELETE", "/windows/"+p2.WindowID, nil)
			if err != nil {
				return nil, err
			}
			return command.TextResult(result), nil
		},
	})
}
```

**Step 4: Create `tabs.go`**

Same pattern as windows. Commands: `list-tabs`, `get-tab`, `create-tab`,
`update-tab`, `close-tab`.

```go
package tools

import (
	"context"
	"encoding/json"

	"github.com/amarbel-llc/purse-first/libs/go-mcp/command"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/protocol"

	"code.linenisgreat.com/chrest/go/src/delta/proxy"
)

func registerTabTools(app *command.App, p *proxy.Proxy) {
	app.AddCommand(&command.Command{
		Name:        "list-tabs",
		Description: command.Description{Short: "List all tabs across all windows"},
		Annotations: &protocol.ToolAnnotations{ReadOnlyHint: protocol.BoolPtr(true)},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			result, err := p.RequestAllBrowsers(ctx, "GET", "/tabs", nil)
			if err != nil {
				return nil, err
			}
			return command.TextResult(result), nil
		},
	})

	app.AddCommand(&command.Command{
		Name:        "get-tab",
		Description: command.Description{Short: "Get details of a specific tab by ID"},
		Annotations: &protocol.ToolAnnotations{ReadOnlyHint: protocol.BoolPtr(true)},
		Params: []command.Param{
			{Name: "tab_id", Type: command.String, Required: true, Description: "Tab ID"},
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var p2 struct{ TabID string `json:"tab_id"` }
			if err := json.Unmarshal(args, &p2); err != nil {
				return command.TextErrorResult(err.Error()), nil
			}
			result, err := p.RequestAllBrowsers(ctx, "GET", "/tabs/"+p2.TabID, nil)
			if err != nil {
				return nil, err
			}
			return command.TextResult(result), nil
		},
	})

	app.AddCommand(&command.Command{
		Name:        "create-tab",
		Description: command.Description{Short: "Create a new tab with the specified URL"},
		Params: []command.Param{
			{Name: "url", Type: command.String, Required: true, Description: "URL to open"},
			{Name: "window_id", Type: command.String, Description: "Window ID to create the tab in"},
			{Name: "active", Type: command.Bool, Description: "Whether to make the tab active"},
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var p2 struct {
				URL      string `json:"url"`
				WindowID string `json:"window_id,omitempty"`
				Active   bool   `json:"active,omitempty"`
			}
			if err := json.Unmarshal(args, &p2); err != nil {
				return command.TextErrorResult(err.Error()), nil
			}
			body := map[string]any{"url": p2.URL}
			if p2.WindowID != "" {
				body["windowId"] = p2.WindowID
			}
			if p2.Active {
				body["active"] = true
			}
			result, err := p.RequestAllBrowsers(ctx, "POST", "/tabs", body)
			if err != nil {
				return nil, err
			}
			return command.TextResult(result), nil
		},
	})

	app.AddCommand(&command.Command{
		Name:        "update-tab",
		Description: command.Description{Short: "Update a tab's URL or other properties"},
		Params: []command.Param{
			{Name: "tab_id", Type: command.String, Required: true, Description: "Tab ID"},
			{Name: "url", Type: command.String, Description: "New URL"},
			{Name: "active", Type: command.Bool, Description: "Whether to make the tab active"},
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var p2 struct {
				TabID  string `json:"tab_id"`
				URL    string `json:"url,omitempty"`
				Active bool   `json:"active,omitempty"`
			}
			if err := json.Unmarshal(args, &p2); err != nil {
				return command.TextErrorResult(err.Error()), nil
			}
			body := map[string]any{}
			if p2.URL != "" {
				body["url"] = p2.URL
			}
			if p2.Active {
				body["active"] = true
			}
			result, err := p.RequestAllBrowsers(ctx, "PATCH", "/tabs/"+p2.TabID, body)
			if err != nil {
				return nil, err
			}
			return command.TextResult(result), nil
		},
	})

	app.AddCommand(&command.Command{
		Name:        "close-tab",
		Description: command.Description{Short: "Close a tab by ID"},
		Annotations: &protocol.ToolAnnotations{DestructiveHint: protocol.BoolPtr(true)},
		Params: []command.Param{
			{Name: "tab_id", Type: command.String, Required: true, Description: "Tab ID"},
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var p2 struct{ TabID string `json:"tab_id"` }
			if err := json.Unmarshal(args, &p2); err != nil {
				return command.TextErrorResult(err.Error()), nil
			}
			result, err := p.RequestAllBrowsers(ctx, "DELETE", "/tabs/"+p2.TabID, nil)
			if err != nil {
				return nil, err
			}
			return command.TextResult(result), nil
		},
	})
}
```

**Step 5: Create `items.go`**

The `items-get` and `items-put` commands use the `browser_items` package
directly via `Proxy` (reusing existing `BrowserProxy` logic). These serve
both CLI and MCP.

```go
package tools

import (
	"context"
	"encoding/json"

	"github.com/amarbel-llc/purse-first/libs/go-mcp/command"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/protocol"

	"code.linenisgreat.com/chrest/go/src/charlie/browser_items"
	"code.linenisgreat.com/chrest/go/src/delta/proxy"
	"code.linenisgreat.com/dodder/go/lib/bravo/errors"
)

func registerItemTools(app *command.App, p *proxy.Proxy) {
	app.AddCommand(&command.Command{
		Name:        "items-get",
		Description: command.Description{Short: "Get browser items (tabs, bookmarks, history)"},
		Annotations: &protocol.ToolAnnotations{ReadOnlyHint: protocol.BoolPtr(true)},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			socks, err := p.GetSockets()
			if err != nil {
				return nil, errors.Wrap(err)
			}

			bp := browser_items.BrowserProxy{Config: p.Config}
			resp, err := bp.GetForSockets(ctx, browser_items.BrowserRequestGet{}, socks)
			if err != nil {
				return nil, errors.Wrap(err)
			}

			return command.JSONResult(resp.RequestPayloadGet), nil
		},
	})

	app.AddCommand(&command.Command{
		Name:        "items-put",
		Description: command.Description{Short: "Add, delete, or focus browser items"},
		Params: []command.Param{
			{Name: "added", Type: command.Array, Description: "Items to add (objects with url, title)"},
			{Name: "deleted", Type: command.Array, Description: "Items to delete (objects with id)"},
			{Name: "focused", Type: command.Array, Description: "Items to focus (objects with id)"},
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var p2 struct {
				Added   []itemArg `json:"added,omitempty"`
				Deleted []itemArg `json:"deleted,omitempty"`
				Focused []itemArg `json:"focused,omitempty"`
			}
			if err := json.Unmarshal(args, &p2); err != nil {
				return command.TextErrorResult(err.Error()), nil
			}

			browserReq := browser_items.BrowserRequestPut{
				Added:   make([]browser_items.Item, 0, len(p2.Added)),
				Deleted: make([]browser_items.Item, 0, len(p2.Deleted)),
				Focused: make([]browser_items.Item, 0, len(p2.Focused)),
			}

			for _, item := range p2.Added {
				var url browser_items.Url
				url.Set(item.URL)
				browserReq.Added = append(browserReq.Added, browser_items.Item{
					Url:   url,
					Title: item.Title,
				})
			}
			for _, item := range p2.Deleted {
				browserReq.Deleted = append(browserReq.Deleted, browser_items.Item{
					ExternalId: item.ID,
				})
			}
			for _, item := range p2.Focused {
				browserReq.Focused = append(browserReq.Focused, browser_items.Item{
					ExternalId: item.ID,
				})
			}

			socks, err := p.GetSockets()
			if err != nil {
				return nil, errors.Wrap(err)
			}

			bp := browser_items.BrowserProxy{Config: p.Config}
			resp, err := bp.PutForSockets(ctx, browserReq, socks)
			if err != nil {
				return nil, errors.Wrap(err)
			}

			return command.JSONResult(resp.RequestPayloadPut), nil
		},
	})
}

type itemArg struct {
	ID    string `json:"id,omitempty"`
	URL   string `json:"url,omitempty"`
	Title string `json:"title,omitempty"`
}
```

**Step 6: Create `state.go`**

```go
package tools

import (
	"context"
	"encoding/json"

	"github.com/amarbel-llc/purse-first/libs/go-mcp/command"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/protocol"

	"code.linenisgreat.com/chrest/go/src/delta/proxy"
	"code.linenisgreat.com/dodder/go/lib/bravo/errors"
)

func registerStateTools(app *command.App, p *proxy.Proxy) {
	app.AddCommand(&command.Command{
		Name:        "state-get",
		Description: command.Description{Short: "Get the current browser state for saving/restoring"},
		Annotations: &protocol.ToolAnnotations{ReadOnlyHint: protocol.BoolPtr(true)},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			result, err := p.RequestAllBrowsers(ctx, "GET", "/state", nil)
			if err != nil {
				return nil, err
			}
			return command.TextResult(result), nil
		},
	})

	app.AddCommand(&command.Command{
		Name:        "state-restore",
		Description: command.Description{Short: "Restore a previously saved browser state"},
		Params: []command.Param{
			{Name: "state", Type: command.Object, Required: true, Description: "Previously saved browser state"},
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var p2 struct{ State json.RawMessage `json:"state"` }
			if err := json.Unmarshal(args, &p2); err != nil {
				return command.TextErrorResult(err.Error()), nil
			}
			var state any
			if err := json.Unmarshal(p2.State, &state); err != nil {
				return nil, errors.Wrap(err)
			}
			result, err := p.RequestAllBrowsers(ctx, "POST", "/state", state)
			if err != nil {
				return nil, err
			}
			return command.TextResult(result), nil
		},
	})
}
```

**Step 7: Verify all tool packages compile**

```bash
cd /home/sasha/eng/repos/chrest/.worktrees/free-yew/go
go build ./src/delta/tools/
```

**Step 8: Commit**

```
feat: add command.App tool registration with go-mcp
```

---

### Task 4: Rewrite main.go with command.App dispatch

Replace the switch/case CLI dispatch with `command.App`. The `mcp` subcommand
creates the server. All other subcommands go through `app.RunCLI`.

**Files:**

- Rewrite: `go/cmd/chrest/main.go`

**Step 1: Rewrite main.go**

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"syscall"

	"github.com/amarbel-llc/purse-first/libs/go-mcp/command"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/server"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/transport"

	"code.linenisgreat.com/chrest/go/src/bravo/config"
	"code.linenisgreat.com/chrest/go/src/delta/proxy"
	"code.linenisgreat.com/chrest/go/src/delta/tools"
	"code.linenisgreat.com/dodder/go/lib/_/stack_frame"
	"code.linenisgreat.com/dodder/go/lib/bravo/errors"
	"code.linenisgreat.com/dodder/go/lib/charlie/ui"
)

func init() {
	log.SetPrefix("chrest ")
}

func main() {
	ctx := errors.MakeContextDefault()
	ctx.SetCancelOnSignals(syscall.SIGTERM)

	if err := ctx.Run(
		func(ctx errors.Context) {
			if err := run(ctx); err != nil {
				ctx.Cancel(err)
			}
		},
	); err != nil {
		var normalError stack_frame.ErrorStackTracer

		if errors.As(err, &normalError) && !normalError.ShouldShowStackTrace() {
			ui.Err().Printf("%s", normalError.Error())
		} else {
			if err != nil {
				ui.Err().Print(err)
			}
		}
	}
}

func run(ctx context.Context) (err error) {
	var c config.Config

	if c, err = config.Default(); err != nil {
		err = errors.Wrap(err)
		return
	}

	if err = c.Read(); err != nil {
		err = errors.Wrap(err)
		return
	}

	p := &proxy.Proxy{Config: c}

	app := command.NewApp("chrest", "Manage browsers via REST")
	app.Version = "0.1.0"

	tools.RegisterAll(app, p)
	registerCLICommands(app, c, p)

	// Check for mcp subcommand
	if len(os.Args) > 1 && os.Args[1] == "mcp" {
		return runMCP(ctx, app, os.Args[2:])
	}

	// CLI mode
	if len(os.Args) <= 1 {
		printUsage(app)
		return
	}

	return app.RunCLI(ctx, os.Args[1:], command.StubPrompter{})
}

func runMCP(ctx context.Context, app *command.App, args []string) (err error) {
	// TODO: parse --transport and --port from args
	t := transport.NewStdio(os.Stdin, os.Stdout)

	registry := server.NewToolRegistryV1()
	app.RegisterMCPToolsV1(registry)

	srv, err := server.New(t, server.Options{
		ServerName:    app.Name,
		ServerVersion: app.Version,
		Tools:         registry,
	})
	if err != nil {
		return errors.Wrap(err)
	}

	return srv.Run(ctx)
}

func printUsage(app *command.App) {
	fmt.Println("Usage: chrest <command> [options]")
	fmt.Println()
	fmt.Println("Commands:")
	for name, cmd := range app.VisibleCommands() {
		fmt.Printf("  %-20s %s\n", name, cmd.Description.Short)
	}
	fmt.Println("  mcp                 Run MCP server")
}
```

**Step 2: Verify it compiles**

```bash
cd /home/sasha/eng/repos/chrest/.worktrees/free-yew/go
go build ./cmd/chrest/
```

**Step 3: Commit**

```
feat: rewrite main.go with command.App dispatch
```

---

### Task 5: Migrate CLI-only commands

Port `client`, `reload-extension`, `init`, and add `generate-plugin`. These have
`RunCLI` only (no `Run`), so they won't appear as MCP tools.

**Files:**

- Rewrite: `go/cmd/chrest/client.go` → CLI-only command registration
- Rewrite: `go/cmd/chrest/reload_extension.go` → CLI-only command registration
- Rewrite: `go/cmd/chrest/init.go` → CLI-only command registration
- Create: `go/cmd/chrest/cli_commands.go` (registers all CLI-only commands)
- Delete: `go/cmd/chrest/items_get.go`
- Delete: `go/cmd/chrest/items_put.go`
- Delete: `go/cmd/chrest/mcp.go`
- Delete: `go/cmd/chrest/browser_flags.go`

**Step 1: Create `cli_commands.go`**

This file registers CLI-only commands and re-exports the remaining CLI files.
The existing `client.go`, `reload_extension.go`, and `init.go` keep their
implementation logic but register via `app.AddCommand` with `RunCLI` instead of
being called from the switch/case.

```go
package main

import (
	"code.linenisgreat.com/chrest/go/src/bravo/config"
	"code.linenisgreat.com/chrest/go/src/delta/proxy"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/command"
)

func registerCLICommands(app *command.App, c config.Config, p *proxy.Proxy) {
	registerClientCommand(app, c)
	registerReloadExtensionCommand(app, c)
	registerInitCommand(app)
	registerGeneratePluginCommand(app)
}
```

The individual command files (`client.go`, `reload_extension.go`, `init.go`)
should be rewritten to expose a `registerXCommand(app)` function that adds a
`command.Command` with `RunCLI`. The existing implementation logic stays, just
wrapped in the new structure.

`generate-plugin` is:

```go
func registerGeneratePluginCommand(app *command.App) {
	app.AddCommand(&command.Command{
		Name:   "generate-plugin",
		Hidden: true,
		Description: command.Description{
			Short: "Generate purse-first plugin artifacts",
		},
		Params: []command.Param{
			{Name: "dir", Type: command.String, Required: true, Description: "Output directory"},
		},
		RunCLI: func(ctx context.Context, args json.RawMessage) error {
			var p struct{ Dir string `json:"dir"` }
			json.Unmarshal(args, &p)
			return app.GenerateAll(p.Dir)
		},
	})
}
```

**Step 2: Delete old files**

Delete `items_get.go`, `items_put.go`, `mcp.go`, `browser_flags.go` — their
functionality is now in `delta/tools/` and the new `main.go`.

**Step 3: Verify build**

```bash
cd /home/sasha/eng/repos/chrest/.worktrees/free-yew/go
go build ./cmd/chrest/
```

**Step 4: Commit**

```
feat: migrate CLI-only commands to command.App, remove old dispatch
```

---

### Task 6: Delete old delta/mcp package

**Files:**

- Delete: `go/src/delta/mcp/server.go`
- Delete: `go/src/delta/mcp/tools.go`
- Delete: `go/src/delta/mcp/handlers.go`
- Delete: `go/src/delta/mcp/scopes.go`

**Step 1: Delete all files in delta/mcp/**

```bash
rm -r /home/sasha/eng/repos/chrest/.worktrees/free-yew/go/src/delta/mcp/
```

**Step 2: Verify build**

```bash
cd /home/sasha/eng/repos/chrest/.worktrees/free-yew/go
go build ./cmd/chrest/
```

**Step 3: Commit**

```
refactor: remove old go-sdk MCP package
```

---

### Task 7: Remove MCPConfig from config package

The scope system is gone, so `MCPConfig` is unused.

**Files:**

- Modify: `go/src/bravo/config/main.go`

**Step 1: Remove MCPConfig struct and MCP field from Config**

Remove the `MCPConfig` struct and the `MCP MCPConfig` field from `Config`.

**Step 2: Verify build**

```bash
cd /home/sasha/eng/repos/chrest/.worktrees/free-yew/go
go build ./...
```

**Step 3: Commit**

```
refactor: remove MCPConfig scope system from config
```

---

### Task 8: Update flake.nix — remove install-mcp, add generate-plugin

**Files:**

- Modify: `go/flake.nix`

**Step 1: Remove the `apps.install-mcp` block**

**Step 2: Add postInstall to chrest package**

Add `postInstall = "$out/bin/chrest generate-plugin $out";` to the
`buildGoApplication` call so `plugin.json` is generated at build time.

**Step 3: Verify nix build**

```bash
just build-nix
```

**Step 4: Commit**

```
feat: replace install-mcp with generate-plugin in flake.nix
```

---

### Task 9: Update gomod2nix.toml

**Files:**

- Modify: `go/gomod2nix.toml`

**Step 1: Regenerate gomod2nix**

```bash
cd /home/sasha/eng/repos/chrest/.worktrees/free-yew/go
gomod2nix
```

**Step 2: Verify nix build**

```bash
just build-nix
```

**Step 3: Commit**

```
chore: regenerate gomod2nix.toml for go-mcp dependency
```

---

### Task 10: Verify end-to-end

**Step 1: Build**

```bash
cd /home/sasha/eng/repos/chrest/.worktrees/free-yew
just build
```

**Step 2: Run MCP server manually and verify tools/list**

```bash
echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"0.1.0"}}}' | ./go/build/debug/chrest mcp
```

Verify it responds with server info and capabilities including tools.

**Step 3: Verify CLI commands still work**

```bash
./go/build/debug/chrest browser-info
./go/build/debug/chrest list-tabs
```

**Step 4: Update CLAUDE.md if any command names changed**

The `chrest mcp` command stays the same. CLI commands like `items-get` keep
their names. No CLAUDE.md changes needed unless command surface changed.

**Step 5: Commit if any fixups needed**

```
fix: end-to-end verification fixups
```
