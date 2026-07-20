package config

import (
	"fmt"
	"os"
	"path"
	"path/filepath"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

type Config struct {
	DefaultBrowser BrowserId   `json:"default-browser"`
	LoadedBrowsers []BrowserId `json:"-"`
	Home           string      `json:"-"`
}

func StateDirectory() (value string, err error) {
	value = os.Getenv("XDG_STATE_HOME")

	if value == "" {
		if value, err = os.UserHomeDir(); err != nil {
			err = errors.Wrap(err)
			return
		}

		value = path.Join(value, ".local", "state")
	}

	value = path.Join(value, "chrest")

	return
}

func (config Config) GetSocketPathForBrowserId(
	id BrowserId,
) (sock string, err error) {
	if id.IsEmpty() {
		id = config.DefaultBrowser
	}

	var stateDir string

	if stateDir, err = StateDirectory(); err != nil {
		err = errors.Wrap(err)
		return
	}

	sock = path.Join(stateDir, fmt.Sprintf("%s.sock", id))

	return
}

func (config Config) GetAllSockets() (socks []string, err error) {
	var stateDir string

	if stateDir, err = StateDirectory(); err != nil {
		err = errors.Wrap(err)
		return
	}

	if socks, err = filepath.Glob(filepath.Join(stateDir, "*.sock")); err != nil {
		err = errors.Wrap(err)
		return
	}

	return
}

func (config Config) Directory() (v string) {
	v = os.Getenv("XDG_CONFIG_HOME")

	if v == "" {
		v = path.Join(config.Home, ".config")
	}

	v = path.Join(v, "chrest")

	return
}
