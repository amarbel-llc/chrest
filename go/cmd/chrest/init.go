package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"code.linenisgreat.com/chrest/go/internal/bravo/config"
	"code.linenisgreat.com/chrest/go/internal/charlie/install"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/command"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
	tap "github.com/amarbel-llc/tap/go/pkgs/writer"
)

const defaultBrowserName = "default"

func registerInitCommand(app *command.Utility) {
	browser := command.StringFlag{}
	browser.Name = "browser"
	browser.Description = "Default browser (chrome or firefox)"
	browser.EnumValues = []string{"chrome", "firefox"}

	name := command.StringFlag{}
	name.Name = "name"
	name.Description = "Browser instance name (default: \"default\")"

	extensionID := command.StringFlag{}
	extensionID.Name = "extension-id"
	extensionID.Description = "Custom extension ID (uses default if omitted)"

	app.AddCommand(&command.Command{
		Name: "init",
		Description: command.Description{
			Short: "Initialize configuration and install native messaging host",
		},
		Params: []command.Param{browser, name, extensionID},
		Run: func(ctx context.Context, args json.RawMessage, p command.Prompter) (*command.Result, error) {
			return nil, cmdInit(ctx, args, p)
		},
	})
}

func cmdInit(ctx context.Context, args json.RawMessage, p command.Prompter) (err error) {
	var params struct {
		Browser     string `json:"browser"`
		Name        string `json:"name"`
		ExtensionId string `json:"extension-id"`
	}

	if err = json.Unmarshal(args, &params); err != nil {
		err = errors.Wrap(err)
		return
	}

	if params.Browser == "" {
		var idx int
		options := []string{"chrome", "firefox"}

		if idx, err = p.Select("Default browser", options); err != nil {
			err = errors.Errorf("--browser is required when not interactive")
			return
		}

		params.Browser = options[idx]
	}

	usedDefaultName := false

	if params.Name == "" {
		params.Name = defaultBrowserName
		usedDefaultName = true
	}

	bid := fmt.Sprintf("%s-%s", params.Browser, params.Name)

	w := tap.NewColorWriter(os.Stderr, true)
	defer w.Plan()

	var initConfig config.Config

	if initConfig, err = config.Default(); err != nil {
		w.NotOk("Read default config", map[string]string{"error": err.Error()})
		err = errors.Wrap(err)
		return
	}

	if err = initConfig.DefaultBrowser.Set(bid); err != nil {
		w.NotOk(
			fmt.Sprintf("Set default browser to %s", bid),
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

	nameNote := ""
	if usedDefaultName {
		nameNote = " (using default name)"
	}

	w.Ok(fmt.Sprintf("Wrote config to %s (browser: %s)%s", initConfig.Directory(), bid, nameNote))

	var exe string

	if exe, err = os.Executable(); err != nil {
		w.NotOk("Find executable path", map[string]string{"error": err.Error()})
		err = errors.Wrap(err)
		return
	}

	serverBinary := filepath.Join(filepath.Dir(exe), "chrest-server")

	if _, err = os.Stat(serverBinary); err != nil {
		w.NotOk(
			fmt.Sprintf("Find chrest-server binary at %s", serverBinary),
			map[string]string{"error": err.Error()},
		)
		err = errors.Errorf("chrest-server not found adjacent to chrest at %s", serverBinary)
		return
	}

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

	if _, _, err = idSet.Install(initConfig, serverBinary); err != nil {
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

	w.Comment(
		"Set browser_id to \"%s\" in the extension options page, then reload the extension",
		params.Name,
	)

	return
}
