package client

import (
	"fmt"
	"net/url"

	"code.linenisgreat.com/chrest/go/src/bravo/config"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

type BrowserItem struct {
	Id         BrowserItemId `json:"id"`
	Url        string        `json:"url"`
	Date       string        `json:"date"`
	Title      string        `json:"title"`
	ExternalId string        `json:"external-id"` // external to the browser, so for us, it's actually our id
}

func (i BrowserItem) GetUrl() (u *url.URL, err error) {
	ur := i.Url

	if ur == "" {
		err = errors.Errorf("empty url")
		return
	}

	if u, err = url.Parse(ur); err != nil {
		err = errors.Wrap(err)
		return
	}

	return
}

type BrowserItemId struct {
	BrowserId config.BrowserId `json:"browser-id"`
	Id        string           `json:"id"`
	Type      string           `json:"type"`
}

func (bi BrowserItemId) String() string {
	return fmt.Sprintf("/%s/%s-%s", bi.BrowserId, bi.Type, bi.Id)
}
