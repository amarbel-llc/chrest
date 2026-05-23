package tools

import (
	"code.linenisgreat.com/chrest/go/internal/charlie/browser_items"
	"code.linenisgreat.com/chrest/go/internal/delta/proxy"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/command"
)

func RegisterAll(app *command.Utility, p *proxy.BrowserProxy) {
	itemsProxy := browser_items.BrowserProxy{Config: p.Config}

	registerBrowserCommands(app, p)
	registerWindowCommands(app, p)
	registerTabCommands(app, p)
	registerItemCommands(app, p, itemsProxy)
	registerStateCommands(app, p)
	registerCaptureCommands(app, p)
}
