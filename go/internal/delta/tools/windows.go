package tools

import (
	"context"
	"encoding/json"

	"code.linenisgreat.com/chrest/go/internal/charlie/proxy"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/command"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/protocol"
)

func registerWindowCommands(app *command.Utility, p *proxy.BrowserProxy) {
	windowID := command.StringFlag{}
	windowID.Name = "window_id"
	windowID.Required = true
	windowID.Description = "Window ID"

	focused := command.BoolFlag{}
	focused.Name = "focused"
	focused.Description = "Whether the window should be focused"

	app.AddCommand(&command.Command{
		Name:        "list-windows",
		Description: command.Description{Short: "List all browser windows"},
		Annotations: &protocol.ToolAnnotations{ReadOnlyHint: protocol.BoolPtr(true)},
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
		Description: command.Description{Short: "Get a specific browser window by ID"},
		Annotations: &protocol.ToolAnnotations{ReadOnlyHint: protocol.BoolPtr(true)},
		Params:      []command.Param{windowID},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var p0 struct {
				WindowID string `json:"window_id"`
			}
			if err := json.Unmarshal(args, &p0); err != nil {
				return command.TextErrorResult(err.Error()), nil
			}
			result, err := p.RequestAllBrowsers(ctx, "GET", "/windows/"+p0.WindowID, nil)
			if err != nil {
				return nil, err
			}
			return command.TextResult(result), nil
		},
	})

	urls := command.ArrayFlag{
		Name:        "urls",
		Description: "URLs to open in the new window",
	}

	incognito := command.BoolFlag{}
	incognito.Name = "incognito"
	incognito.Description = "Whether to create an incognito window"

	app.AddCommand(&command.Command{
		Name:        "create-window",
		Description: command.Description{Short: "Create a new browser window"},
		Params:      []command.Param{urls, focused, incognito},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var p0 struct {
				URLs      []string `json:"urls"`
				Focused   bool     `json:"focused"`
				Incognito bool     `json:"incognito"`
			}
			if err := json.Unmarshal(args, &p0); err != nil {
				return command.TextErrorResult(err.Error()), nil
			}

			body := map[string]any{}
			if len(p0.URLs) > 0 {
				body["url"] = p0.URLs
			}
			if p0.Focused {
				body["focused"] = true
			}
			if p0.Incognito {
				body["incognito"] = true
			}

			result, err := p.RequestAllBrowsers(ctx, "POST", "/windows", body)
			if err != nil {
				return nil, err
			}
			return command.TextResult(result), nil
		},
	})

	state := command.StringFlag{}
	state.Name = "state"
	state.Description = "Window state (normal, minimized, maximized, fullscreen)"
	state.EnumValues = []string{"normal", "minimized", "maximized", "fullscreen"}

	app.AddCommand(&command.Command{
		Name:        "update-window",
		Description: command.Description{Short: "Update a browser window"},
		Params:      []command.Param{windowID, focused, state},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var p0 struct {
				WindowID string `json:"window_id"`
				Focused  bool   `json:"focused"`
				State    string `json:"state"`
			}
			if err := json.Unmarshal(args, &p0); err != nil {
				return command.TextErrorResult(err.Error()), nil
			}

			body := map[string]any{}
			if p0.Focused {
				body["focused"] = true
			}
			if p0.State != "" {
				body["state"] = p0.State
			}

			result, err := p.RequestAllBrowsers(ctx, "PUT", "/windows/"+p0.WindowID, body)
			if err != nil {
				return nil, err
			}
			return command.TextResult(result), nil
		},
	})

	app.AddCommand(&command.Command{
		Name:        "close-window",
		Description: command.Description{Short: "Close a browser window"},
		Annotations: &protocol.ToolAnnotations{DestructiveHint: protocol.BoolPtr(true)},
		Params:      []command.Param{windowID},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var p0 struct {
				WindowID string `json:"window_id"`
			}
			if err := json.Unmarshal(args, &p0); err != nil {
				return command.TextErrorResult(err.Error()), nil
			}
			result, err := p.RequestAllBrowsers(ctx, "DELETE", "/windows/"+p0.WindowID, nil)
			if err != nil {
				return nil, err
			}
			return command.TextResult(result), nil
		},
	})
}
