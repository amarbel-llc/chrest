package main

import (
	"context"
	"encoding/json"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/command"
)

func registerInstallMCPCommand(app *command.Utility) {
	app.AddCommand(&command.Command{
		Name: "install-mcp",
		Description: command.Description{
			Short: "Install chrest as a Claude Code MCP server",
		},
		RunCLI: func(ctx context.Context, args json.RawMessage) error {
			return app.InstallMCP()
		},
	})
}
