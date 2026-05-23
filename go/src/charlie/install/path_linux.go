package install

import (
	"code.linenisgreat.com/chrest/go/src/alfa/browser"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

func GetUserPath(b browser.Browser) string {
	switch b {
	case browser.Chrome:
		return ".config/google-chrome/NativeMessagingHosts"

	case browser.Chromium:
		return ".config/chromium/NativeMessagingHosts"

	case browser.Firefox:
		return ".mozilla/native-messaging-hosts"

	default:
		panic(errors.Errorf("unsupported browser: %s", b))
	}
}
