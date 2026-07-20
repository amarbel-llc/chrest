package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"code.linenisgreat.com/chrest/go/internal/bravo/config"
	"code.linenisgreat.com/chrest/go/internal/delta/client"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/command"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/primordial"
)

func registerClientCommand(app *command.Utility, c config.Config) {
	browserFlag := command.StringFlag{}
	browserFlag.Name = "browser"
	browserFlag.Description = "Which browser to communicate with"
	browserFlag.Default = os.Getenv("CHREST_BROWSER")

	fullRequestFlag := command.BoolFlag{}
	fullRequestFlag.Name = "full-request"
	fullRequestFlag.Description = "Print the full request including headers"

	httpieArgs := command.StringArg{}
	httpieArgs.Name = "args"
	httpieArgs.Variadic = true
	httpieArgs.Description = "HTTPie arguments (e.g. GET /windows)"

	app.AddCommand(&command.Command{
		Name:        "client",
		Description: command.Description{Short: "Forward HTTP request from stdin to browser"},
		Params:      []command.Param{browserFlag, fullRequestFlag, httpieArgs},
		RunCLI: func(ctx context.Context, args json.RawMessage) error {
			var params struct {
				Browser     string   `json:"browser"`
				FullRequest bool     `json:"full-request"`
				Args        []string `json:"args"`
			}

			if err := json.Unmarshal(args, &params); err != nil {
				return errors.Wrap(err)
			}

			return cmdClient(c, params.Browser, params.FullRequest, params.Args)
		},
	})
}

func cmdClient(c config.Config, browser string, fullRequest bool, httpieArgs []string) (err error) {
	var bid config.BrowserId
	if browser != "" {
		if err = bid.Set(browser); err != nil {
			err = errors.Wrap(err)
			return
		}
	}

	var sock string
	if sock, err = c.GetSocketPathForBrowserId(bid); err != nil {
		err = errors.Wrap(err)
		return
	}

	if err = cmdClientOneSocket(sock, fullRequest, httpieArgs); err != nil {
		if errors.IsErrno(err, syscall.ECONNREFUSED) {
			if err = os.Remove(sock); err != nil {
				err = errors.Wrap(err)
				return
			}
		} else {
			err = errors.Wrap(err)
			return
		}
	}

	return
}

func cmdClientOneSocket(sock string, fullRequest bool, httpieArgs []string) (err error) {
	cmdHttpArgs := append([]string{"--offline"}, httpieArgs...)
	cmdHttpArgs[2] = filepath.Join("localhost", cmdHttpArgs[2])
	cmdHttp := exec.Command("http", cmdHttpArgs...)
	cmdHttp.Stdin = os.Stdin

	var httpieStdout, httpieStderr io.ReadCloser

	if httpieStdout, err = cmdHttp.StdoutPipe(); err != nil {
		err = errors.Wrap(err)
		return
	}

	if httpieStderr, err = cmdHttp.StderrPipe(); err != nil {
		err = errors.Wrap(err)
		return
	}

	// TODO error message when http is missing
	if err = cmdHttp.Start(); err != nil {
		err = errors.Wrap(err)
		return
	}

	if fullRequest {
		if _, err = io.Copy(os.Stdout, httpieStdout); err != nil {
			err = errors.Errorf("failed to write request to stdout: %w", err)
			return
		}

		if _, err = io.Copy(os.Stderr, httpieStderr); err != nil {
			err = errors.Errorf("failed to write request to stdout: %w", err)
			return
		}

		if err = cmdHttp.Wait(); err != nil {
			err = errors.Errorf("httpie failed: %w", err)
			return
		}

		return
	}

	var resp *http.Response

	var conn net.Conn

	if conn, err = net.Dial("unix", sock); err != nil {
		err = errors.Wrap(err)
		return
	}

	if resp, err = client.ResponseFromReader(httpieStdout, conn); err != nil {
		err = errors.Wrapf(err, "Socket: %q", sock)
		return
	}

	if err = cmdHttp.Wait(); err != nil {
		err = errors.Errorf("waiting for httpie failed: %w", err)
		return
	}

	if primordial.IsTty(os.Stdout) {
		for k, vs := range resp.Header {
			for _, v := range vs {
				fmt.Printf("%s: %s\n", k, v)
			}
		}

		fmt.Println()
	}

	cmdJq := exec.Command("jq")
	cmdJq.Stdin = resp.Body
	cmdJq.Stdout = os.Stdout

	// TODO error message when jq is missing
	if err = cmdJq.Run(); err != nil {
		if errors.IsBrokenPipe(err) {
			err = nil
		} else {
			err = errors.Wrap(err)
		}

		return
	}

	if resp.StatusCode >= 400 {
		err = errors.Errorf("http error: %s", http.StatusText(resp.StatusCode))
		return
	}

	return
}
