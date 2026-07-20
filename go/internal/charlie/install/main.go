package install

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"code.linenisgreat.com/chrest/go/internal/0/browser"
	"code.linenisgreat.com/chrest/go/internal/bravo/config"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/pool"
)

type IdSet struct {
	browser.Browser
	Ids []string
}

func (idSet IdSet) String() string {
	var sb strings.Builder

	for i, value := range idSet.Ids {
		if i > 0 {
			sb.WriteRune(',')
			sb.WriteString(value)
		}
	}

	return sb.String()
}

func (idSet *IdSet) Set(value string) (err error) {
	idSet.Ids = append(idSet.Ids, value)
	return
}

func (idSet IdSet) GetDefaultId() string {
	switch idSet.Browser {
	case browser.Chrome, browser.Chromium:
		// TODO compiler var
		return "faeaeoifckcedjniagocclagdbbkifgo"

	case browser.Firefox:
		return "chrest@code.linenisgreat.com"

	default:
		panic(errors.Errorf("unsupported browser: %s", idSet.Browser))
	}
}

type (
	JSONCommon struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Path        string `json:"path"`
		Type        string `json:"type"`
	}

	JSONFirefox struct {
		JSONCommon
		AllowedExtensions []string `json:"allowed_extensions"`
	}

	JSONChromeOrChromium struct {
		JSONCommon
		AllowedOrigins []string `json:"allowed_origins"`
	}
)

func (idSet *IdSet) Install(
	config config.Config,
	serverPath string,
) (loc string, insallJSON any, err error) {
	dir := path.Join(
		config.Home,
		GetUserPath(idSet.Browser),
	)

	loc = path.Join(
		dir,
		"com.linenisgreat.code.chrest.json",
	)

	if loc, err = filepath.Abs(loc); err != nil {
		err = errors.Wrap(err)
		return
	}

	var file *os.File

	if file, err = os.OpenFile(loc, os.O_RDWR|os.O_CREATE, 0o666); err != nil {
		err = errors.Wrap(err)
		return
	}

	bufferedReader, repool := pool.GetBufferedReader(file)
	defer repool()

	dec := json.NewDecoder(bufferedReader)

	defer errors.DeferredCloser(&err, file)

	idSet.Ids = append(idSet.Ids, idSet.GetDefaultId())

	common := JSONCommon{
		Name:        "com.linenisgreat.code.chrest",
		Description: "HTTP or socket server for management",
		Path:        serverPath,
		Type:        "stdio",
	}

	switch idSet.Browser {
	case browser.Chrome, browser.Chromium:
		var existing JSONChromeOrChromium

		if err = dec.Decode(&existing); err != nil {
			if errors.IsEOF(err) {
				err = nil
			} else {
				err = errors.Wrap(err)
				return
			}
		}

		for _, id := range idSet.Ids {
			existing.AllowedOrigins = append(
				existing.AllowedOrigins,
				fmt.Sprintf("chrome-extension://%s/", id),
			)
		}

		slices.Sort(existing.AllowedOrigins)
		existing.AllowedOrigins = slices.Compact(existing.AllowedOrigins)

		existing.JSONCommon = common
		insallJSON = existing

	case browser.Firefox:
		var existing JSONFirefox

		if err = dec.Decode(&existing); err != nil {
			if errors.IsEOF(err) {
				err = nil
			} else {
				err = errors.Wrap(err)
				return
			}
		}

		existing.AllowedExtensions = append(existing.AllowedExtensions, idSet.Ids...)
		slices.Sort(existing.AllowedExtensions)
		existing.AllowedExtensions = slices.Compact(existing.AllowedExtensions)

		existing.JSONCommon = common
		insallJSON = existing

	default:
		err = errors.Errorf("unsupported browser: %s", idSet.Browser)
		return
	}

	var bites []byte

	if bites, err = json.Marshal(insallJSON); err != nil {
		err = errors.Wrap(err)
		return
	}

	if err = os.MkdirAll(dir, 0o700); err != nil {
		err = errors.Wrap(err)
		return
	}

	if err = os.WriteFile(loc, bites, 0o666); err != nil {
		err = errors.Wrap(err)
		return
	}

	return
}
