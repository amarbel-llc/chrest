package config

import (
	"os"

	"code.linenisgreat.com/chrest/go/internal/alfa/browser"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

func Default() (config Config, err error) {
	config.DefaultBrowser.Browser = browser.Firefox

	if config.Home, err = os.UserHomeDir(); err != nil {
		err = errors.Wrap(err)
		return
	}

	return
}
