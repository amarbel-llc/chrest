package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"syscall"
	"time"

	"code.linenisgreat.com/chrest/go/internal/bravo/config"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/command"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

func registerReloadExtensionCommand(app *command.Utility, c config.Config) {
	app.AddCommand(&command.Command{
		Name:        "reload-extension",
		Description: command.Description{Short: "Reload the browser extension"},
		RunCLI: func(ctx context.Context, args json.RawMessage) error {
			return cmdReloadExtension(c)
		},
	})
}

func requestSocket(sock, method, path string) (resp *http.Response, err error) {
	var conn net.Conn

	if conn, err = net.Dial("unix", sock); err != nil {
		err = errors.Wrap(err)
		return
	}

	defer conn.Close()

	req, _ := http.NewRequest(method, "http://localhost"+path, nil)

	if err = req.Write(conn); err != nil {
		err = errors.Wrap(err)
		return
	}

	if resp, err = http.ReadResponse(bufio.NewReader(conn), req); err != nil {
		err = errors.Wrap(err)
		return
	}

	return
}

func cmdReloadExtension(c config.Config) (err error) {
	var sock string

	if sock, err = c.GetSocketPathForBrowserId(config.BrowserId{}); err != nil {
		err = errors.Wrap(err)
		return
	}

	fmt.Println("reloading server")

	if _, err = requestSocket(sock, "POST", "/runtime/reload"); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			err = nil
		} else {
			err = errors.Wrap(err)
			return
		}
	}

	// The native host process is killed when the extension reloads.
	// Wait for Chrome to relaunch it and for the new socket to appear.
	maxRetries := 30
	for i := range maxRetries {
		fmt.Printf("waiting for server to come back up (%d/%d)\n", i+1, maxRetries)
		time.Sleep(500 * time.Millisecond)

		var resp *http.Response

		if resp, err = requestSocket(sock, "GET", "/"); err != nil {
			if errors.Is(err, io.ErrUnexpectedEOF) ||
				errors.IsErrno(err, syscall.ECONNREFUSED) ||
				errors.IsErrno(err, syscall.ENOENT) {
				err = nil
				continue
			}

			err = errors.Wrap(err)
			return
		}

		resp.Body.Close()
		break
	}

	if err == nil && maxRetries > 0 {
		fmt.Println("extension reloaded successfully")
	}

	return
}
