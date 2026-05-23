package tools

import (
	"context"
	"encoding/json"

	"code.linenisgreat.com/chrest/go/src/delta/proxy"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/command"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/protocol"
)

func registerBrowserCommands(app *command.Utility, p *proxy.BrowserProxy) {
	app.AddCommand(&command.Command{
		Name:        "browser-info",
		Description: command.Description{Short: "Get browser information"},
		Annotations: &protocol.ToolAnnotations{ReadOnlyHint: protocol.BoolPtr(true)},
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
		Description: command.Description{Short: "List installed browser extensions"},
		Annotations: &protocol.ToolAnnotations{ReadOnlyHint: protocol.BoolPtr(true)},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			result, err := p.RequestAllBrowsers(ctx, "GET", "/extensions", nil)
			if err != nil {
				return nil, err
			}
			return command.TextResult(result), nil
		},
	})
}
