package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"

	"code.linenisgreat.com/chrest/go/internal/bravo/config"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/interfaces"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/ui"
)

type Server struct {
	interfaces.ActiveContext
	Address         *net.UnixAddr
	Listener        *net.UnixListener
	HTTPHandlerFunc http.HandlerFunc
}

func (s *Server) Initialize() {
	var msgIAm JSONObject
	var browserId string

	ui.Err().Printf("waiting for id from browser")

	if _, err := ReadFromBrowser(&msgIAm); err != nil {
		s.Cancel(err)
	}

	ui.Err().Printf("read from browser: %q", msgIAm)

	var ok bool

	if browserId, ok = msgIAm["browser_id"].(string); !ok {
		errors.ContextCancelWithErrorf(
			s,
			"expected string `browser_id` but got %T",
			msgIAm["browser_id"],
		)

		return
	}

	var dir string

	{
		var err error

		if dir, err = config.StateDirectory(); err != nil {
			s.Cancel(err)
		}
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		s.Cancel(err)
	}

	pathSock := fmt.Sprintf("%s/%s.sock", dir, browserId)

	ui.Err().Printf("starting server on %q", pathSock)

	{
		var err error

		if s.Address, err = net.ResolveUnixAddr("unix", pathSock); err != nil {
			s.Cancel(err)
		}
	}

	// Remove stale socket from a previous process that was killed without
	// cleanup (e.g. Chrome kills native host on extension reload).
	if _, err := os.Stat(pathSock); err == nil {
		ui.Err().Printf("removing stale socket: %s", pathSock)
		os.Remove(pathSock)
	}

	{
		var err error

		if s.Listener, err = net.ListenUnix("unix", s.Address); err != nil {
			// TODO add sigil error
			s.Cancel(err)
		}
	}

	ui.Err().Printf("listening: %s", pathSock)

	go func() {
		<-s.Done()
		os.Remove(pathSock)
	}()
}

func (s *Server) Serve() {
	handler := s.HTTPHandlerFunc

	if handler == nil {
		handler = http.HandlerFunc(s.ServeHTTP)
	}

	httpServer := http.Server{Handler: handler}

	go func() {
		<-s.Done()
		ui.Err().Print("shutting down")

		ctx, cancel := context.WithTimeoutCause(
			context.Background(),
			1e9, // 1 second
			errors.Errorf("shut down timeout"),
		)

		defer cancel()

		httpServer.Shutdown(ctx)
	}()

	if err := httpServer.Serve(s.Listener); err != nil {
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
			return
		} else {
			s.Cancel(err)
		}
	}

	return
}
