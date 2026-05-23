package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/command"
)

func registerVersionCommand(app *command.Utility) {
	app.AddCommand(&command.Command{
		Name: "version",
		Description: command.Description{
			Short: "Print build identity (version+commit)",
		},
		RunCLI: func(ctx context.Context, args json.RawMessage) error {
			fmt.Printf("%s+%s\n", version, commit)
			return nil
		},
	})
}
