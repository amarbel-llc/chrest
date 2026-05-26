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
			Short: "Print build identity",
		},
		RunCLI: func(ctx context.Context, args json.RawMessage) error {
			// `version` already encodes the dev marker (chrest#61):
			// clean release builds report bare "X.Y.Z"; dirty / dev
			// builds report "X.Y.Z-dev+<shortSha>". `commit` is still
			// populated via -X main.commit for any caller that wants
			// it, just not echoed here to avoid doubling the sha.
			fmt.Println(version)
			return nil
		},
	})
}
