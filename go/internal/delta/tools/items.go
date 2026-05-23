package tools

import (
	"context"
	"encoding/json"

	"code.linenisgreat.com/chrest/go/internal/charlie/browser_items"
	"code.linenisgreat.com/chrest/go/internal/delta/proxy"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/command"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/protocol"
)

type itemArg struct {
	ID    string `json:"id,omitempty"`
	URL   string `json:"url,omitempty"`
	Title string `json:"title,omitempty"`
}

func registerItemCommands(
	app *command.Utility,
	p *proxy.BrowserProxy,
	itemsProxy browser_items.BrowserProxy,
) {
	app.AddCommand(&command.Command{
		Name:        "items-get",
		Description: command.Description{Short: "Get browser items (tabs, bookmarks, history)"},
		Annotations: &protocol.ToolAnnotations{ReadOnlyHint: protocol.BoolPtr(true)},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			socks, err := p.GetSockets()
			if err != nil {
				return nil, err
			}

			resp, err := itemsProxy.GetForSockets(ctx, browser_items.BrowserRequestGet{}, socks)
			if err != nil {
				return nil, err
			}

			jsonBytes, err := json.MarshalIndent(resp.RequestPayloadGet, "", "  ")
			if err != nil {
				return nil, err
			}

			return command.TextResult(string(jsonBytes)), nil
		},
	})

	app.AddCommand(&command.Command{
		Name:        "items-put",
		Description: command.Description{Short: "Add, delete, or focus browser items"},
		Annotations: &protocol.ToolAnnotations{DestructiveHint: protocol.BoolPtr(true)},
		Params: []command.Param{
			command.ArrayFlag{Name: "added", Description: "Items to add (objects with id, url, title)"},
			command.ArrayFlag{Name: "deleted", Description: "Items to delete (objects with id, url, title)"},
			command.ArrayFlag{Name: "focused", Description: "Items to focus (objects with id, url, title)"},
		},
		Run: func(ctx context.Context, args json.RawMessage, _ command.Prompter) (*command.Result, error) {
			var p0 struct {
				Added   []itemArg `json:"added"`
				Deleted []itemArg `json:"deleted"`
				Focused []itemArg `json:"focused"`
			}
			if err := json.Unmarshal(args, &p0); err != nil {
				return command.TextErrorResult(err.Error()), nil
			}

			browserReq := browser_items.BrowserRequestPut{
				Added:   make([]browser_items.Item, 0, len(p0.Added)),
				Deleted: make([]browser_items.Item, 0, len(p0.Deleted)),
				Focused: make([]browser_items.Item, 0, len(p0.Focused)),
			}

			for _, item := range p0.Added {
				var url browser_items.Url
				url.Set(item.URL)
				browserReq.Added = append(browserReq.Added, browser_items.Item{
					Url:   url,
					Title: item.Title,
				})
			}

			for _, item := range p0.Deleted {
				browserReq.Deleted = append(browserReq.Deleted, browser_items.Item{
					ExternalId: item.ID,
				})
			}

			for _, item := range p0.Focused {
				browserReq.Focused = append(browserReq.Focused, browser_items.Item{
					ExternalId: item.ID,
				})
			}

			socks, err := p.GetSockets()
			if err != nil {
				return nil, err
			}

			resp, err := itemsProxy.PutForSockets(ctx, browserReq, socks)
			if err != nil {
				return nil, err
			}

			jsonBytes, err := json.MarshalIndent(resp.RequestPayloadPut, "", "  ")
			if err != nil {
				return nil, err
			}

			return command.TextResult(string(jsonBytes)), nil
		},
	})
}
