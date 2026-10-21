package bookmark_plugin

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"code.linenisgreat.com/chrest/go/internal/bravo/config"
	"code.linenisgreat.com/chrest/go/internal/charlie/browser_items"
)

const (
	socketSuffix = ".sock"
	probeTimeout = 500 * time.Millisecond
	treeTTL      = 5 * time.Second
)

// SocketSource reads bookmark trees from chrest's per-browser unix sockets
// in the state directory. Each browser is addressed by its own socket; there
// is no fan-out. Trees are cached briefly so that listing several levels
// does not refetch the whole tree each time.
type SocketSource struct {
	Dir   string
	Proxy browser_items.BrowserProxy

	mu    sync.Mutex
	cache map[string]cachedTree
}

type cachedTree struct {
	at   time.Time
	tree browser_items.BookmarkTree
}

// NewSocketSource uses chrest's state directory ($XDG_STATE_HOME/chrest).
func NewSocketSource() (*SocketSource, error) {
	dir, err := config.StateDirectory()
	if err != nil {
		return nil, err
	}

	return &SocketSource{Dir: dir}, nil
}

func (s *SocketSource) Browsers(ctx context.Context) ([]string, error) {
	socks, err := filepath.Glob(filepath.Join(s.Dir, "*"+socketSuffix))
	if err != nil {
		return nil, err
	}

	var live []string

	for _, sock := range socks {
		d := net.Dialer{Timeout: probeTimeout}

		conn, err := d.DialContext(ctx, "unix", sock)
		if err != nil {
			fmt.Fprintf(os.Stderr, "bookmark: skipping dead socket %s: %v\n", filepath.Base(sock), err)
			continue
		}

		_ = conn.Close()

		live = append(live, strings.TrimSuffix(filepath.Base(sock), socketSuffix))
	}

	return live, nil
}

func (s *SocketSource) Tree(
	ctx context.Context,
	browserId string,
) (browser_items.BookmarkTree, error) {
	// The id arrives from a URI host; it must name a file in Dir, nothing else.
	if browserId == "" || strings.ContainsAny(browserId, `/\`) || strings.Contains(browserId, "..") {
		return browser_items.BookmarkTree{}, fmt.Errorf("invalid browser id %q", browserId)
	}

	s.mu.Lock()
	if c, ok := s.cache[browserId]; ok && time.Since(c.at) < treeTTL {
		s.mu.Unlock()
		return c.tree, nil
	}
	s.mu.Unlock()

	tree, err := s.Proxy.GetBookmarkTree(ctx, filepath.Join(s.Dir, browserId+socketSuffix))
	if err != nil {
		return browser_items.BookmarkTree{}, err
	}

	s.mu.Lock()
	if s.cache == nil {
		s.cache = map[string]cachedTree{}
	}

	s.cache[browserId] = cachedTree{at: time.Now(), tree: tree}
	s.mu.Unlock()

	return tree, nil
}
