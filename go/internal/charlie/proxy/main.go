package proxy

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"

	"code.linenisgreat.com/chrest/go/internal/bravo/config"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

type BrowserProxy struct {
	Config  config.Config
	Sockets []string // if non-empty, only query these sockets
}

func (p *BrowserProxy) GetSockets() ([]string, error) {
	if len(p.Sockets) > 0 {
		return p.Sockets, nil
	}
	return p.Config.GetAllSockets()
}

func (p *BrowserProxy) RequestAllBrowsers(
	ctx context.Context,
	method string,
	path string,
	body any,
) (string, error) {
	socks, err := p.GetSockets()
	if err != nil {
		return "", errors.Wrap(err)
	}

	if len(socks) == 0 {
		return "[]", nil
	}

	wg := errors.MakeWaitGroupParallel()
	var l sync.Mutex
	var allResults []any
	var sockErrors []string

	for _, sock := range socks {
		wg.Do(func() (err error) {
			result, err := p.requestOneBrowser(ctx, sock, method, path, body)
			if err != nil {
				l.Lock()
				sockErrors = append(sockErrors, fmt.Sprintf("%s: %s", sock, err))
				l.Unlock()
				return nil
			}

			l.Lock()
			defer l.Unlock()

			if arr, ok := result.([]any); ok {
				allResults = append(allResults, arr...)
			} else if result != nil {
				allResults = append(allResults, result)
			}

			return nil
		})
	}

	if err = wg.GetError(); err != nil {
		return "", errors.Wrap(err)
	}

	if len(allResults) == 0 && len(sockErrors) > 0 {
		return "", errors.Errorf(
			"no browsers responded (is the extension running?): %s",
			sockErrors[0],
		)
	}

	if allResults == nil {
		allResults = []any{}
	}

	jsonBytes, err := json.MarshalIndent(allResults, "", "  ")
	if err != nil {
		return "", errors.Wrap(err)
	}

	return string(jsonBytes), nil
}

func (p *BrowserProxy) requestOneBrowser(
	ctx context.Context,
	sock string,
	method string,
	path string,
	body any,
) (any, error) {
	pr, pw := net.Pipe()

	if body != nil {
		go func() {
			json.NewEncoder(pw).Encode(body)
			pw.Close()
		}()
	} else {
		pw.Close()
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, path, pr)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	if body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}

	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", sock)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	defer conn.Close()

	if err = httpReq.Write(conn); err != nil {
		return nil, errors.Wrap(err)
	}

	resp, err := http.ReadResponse(bufio.NewReader(conn), httpReq)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 204 {
		return map[string]string{"status": "success"}, nil
	}

	var result any
	if err = json.NewDecoder(resp.Body).Decode(&result); err != nil {
		if errors.IsEOF(err) {
			return nil, nil
		}
		return nil, errors.Wrap(err)
	}

	return result, nil
}
