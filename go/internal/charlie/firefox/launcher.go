package firefox

import (
	"context"
	"regexp"

	"code.linenisgreat.com/chrest/go/internal/charlie/launcher"
)

func Launch(ctx context.Context) (*launcher.Process, error) {
	return launcher.Launch(ctx, firefoxConfig())
}

// LaunchWithProfile launches headless Firefox against an explicit
// profile directory. The caller owns the directory; the launcher does
// not clean it up.
func LaunchWithProfile(ctx context.Context, profilePath string) (*launcher.Process, error) {
	cfg := firefoxConfig()
	cfg.ProfilePath = profilePath
	return launcher.Launch(ctx, cfg)
}

func firefoxConfig() launcher.BrowserConfig {
	return launcher.BrowserConfig{
		BinaryNames: []string{"firefox", "firefox-esr"},
		Args: []string{
			"--headless",
			"--remote-debugging-port=0",
			"--no-remote",
		},
		URL:         "about:blank",
		TempProfile: "chrest-firefox-",
		Discovery: launcher.DiscoveryStrategy{
			Kind:    launcher.StderrRegex,
			Pattern: regexp.MustCompile(`WebDriver BiDi listening on (ws://\S+)`),
		},
	}
}
