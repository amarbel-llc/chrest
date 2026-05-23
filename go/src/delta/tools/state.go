package tools

import (
	"context"
	"encoding/json"

	"code.linenisgreat.com/chrest/go/src/delta/proxy"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/command"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/protocol"
)

func registerStateCommands(app *command.Utility, p *proxy.BrowserProxy) {
	app.AddCommand(&command.Command{
		Name:        "state-get",
		Description: command.Description{Short: "Get current browser state"},
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
		Description: command.Description{Short: "Restore browser state from a saved snapshot"},
		Annotations: &protocol.ToolAnnotations{DestructiveHint: protocol.BoolPtr(true)},
		Params: []command.Param{
			command.ObjectFlag{Name: "state", Required: true, Description: "Browser state object to restore"},
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var p0 struct {
				State json.RawMessage `json:"state"`
			}
			if err := json.Unmarshal(args, &p0); err != nil {
				return command.TextErrorResult(err.Error()), nil
			}

			var state any
			if err := json.Unmarshal(p0.State, &state); err != nil {
				return command.TextErrorResult(err.Error()), nil
			}

			result, err := p.RequestAllBrowsers(ctx, "POST", "/state", state)
			if err != nil {
				return nil, err
			}
			return command.TextResult(result), nil
		},
	})
}
