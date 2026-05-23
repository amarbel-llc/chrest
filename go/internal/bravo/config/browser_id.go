package config

import (
	"fmt"
	"strings"

	"code.linenisgreat.com/chrest/go/internal/0/browser"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

type BrowserId struct {
	Browser browser.Browser `json:"browser"`
	Id      string          `json:"id"`
}

func (browserId BrowserId) IsEmpty() bool {
	return browserId.Browser == "" && browserId.Id == ""
}

func (browserId *BrowserId) Set(v string) (err error) {
	v0 := v
	v = strings.TrimSpace(strings.ToLower(v))

	head, tail, _ := strings.Cut(v, "-")

	browserId.Id = tail

	if err = browserId.Browser.Set(head); err != nil {
		err = errors.Wrapf(err, "Raw Id: %q", v0)
		return
	}

	return
}

func (browserId BrowserId) String() string {
	var sb strings.Builder

	fmt.Fprintf(&sb, "%s", browserId.Browser)

	if browserId.Id == "" {
		return sb.String()
	}

	fmt.Fprintf(&sb, "-%s", browserId.Id)

	return sb.String()
}
