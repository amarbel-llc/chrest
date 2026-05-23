package config

import (
	"os"
	"path"
	"path/filepath"

	config_toml "code.linenisgreat.com/chrest/go/src/alfa/config_toml"
	"strings"

	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/interfaces"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

func (config *Config) Read() (err error) {
	wg := errors.MakeWaitGroupParallel()

	wg.Do(config.readConfig)
	wg.Do(config.readLoadedBrowsers)

	if err = wg.GetError(); err != nil {
		err = errors.Wrap(err)
		return
	}

	return
}

func (config *Config) readConfig() (err error) {
	if config.Home, err = os.UserHomeDir(); err != nil {
		err = errors.Wrap(err)
		return
	}

	p := path.Join(config.Directory(), "config.toml")

	var data []byte

	if data, err = os.ReadFile(p); err != nil {
		if errors.IsNotExist(err) {
			err = nil
		} else {
			err = errors.Wrap(err)
		}

		return
	}

	var doc *config_toml.ConfigDocument

	if doc, err = config_toml.DecodeConfig(data); err != nil {
		err = errors.Wrap(err)
		return
	}

	config.DefaultBrowser.Browser = doc.Data().DefaultBrowser.Browser
	config.DefaultBrowser.Id = doc.Data().DefaultBrowser.Id

	return
}

func (config *Config) readLoadedBrowsers() (err error) {
	var loadedBrowserPaths []string

	var stateDir string

	if stateDir, err = StateDirectory(); err != nil {
		err = errors.Wrap(err)
		return
	}

	if loadedBrowserPaths, err = filepath.Glob(filepath.Join(stateDir, "*.sock")); err != nil {
		err = errors.Wrap(err)
		return
	}

	for _, loadedBrowserPath := range loadedBrowserPaths {
		var id BrowserId

		base := filepath.Base(loadedBrowserPath)
		name := strings.TrimSuffix(base, filepath.Ext(base))

		if err = id.Set(name); err != nil {
			err = errors.Wrap(err)
			return
		}

		config.LoadedBrowsers = append(config.LoadedBrowsers, id)
	}

	return
}

func (config *Config) Write(_ interfaces.ActiveContext) (err error) {
	dir := config.Directory()
	p := path.Join(dir, "config.toml")

	// Read existing file for round-trip fidelity, or start from empty.
	var existing []byte

	if existing, err = os.ReadFile(p); err != nil {
		if !errors.IsNotExist(err) {
			err = errors.Wrap(err)
			return
		}

		existing = nil
		err = nil
	}

	var doc *config_toml.ConfigDocument

	if doc, err = config_toml.DecodeConfig(existing); err != nil {
		err = errors.Wrap(err)
		return
	}

	doc.Data().DefaultBrowser.Browser = config.DefaultBrowser.Browser
	doc.Data().DefaultBrowser.Id = config.DefaultBrowser.Id

	var encoded []byte

	if encoded, err = doc.Encode(); err != nil {
		err = errors.Wrap(err)
		return
	}

	if err = os.MkdirAll(dir, 0o700); err != nil {
		err = errors.Wrap(err)
		return
	}

	var tmp *os.File

	if tmp, err = os.CreateTemp("", "chrest-config-*.toml"); err != nil {
		err = errors.Wrap(err)
		return
	}

	tmpName := tmp.Name()

	if _, err = tmp.Write(encoded); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		err = errors.Wrap(err)
		return
	}

	if err = tmp.Close(); err != nil {
		os.Remove(tmpName)
		err = errors.Wrap(err)
		return
	}

	if err = os.Rename(tmpName, p); err != nil {
		os.Remove(tmpName)
		err = errors.Wrap(err)
		return
	}

	return
}
