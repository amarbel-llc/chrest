package main

import (
	"log"
	"os"
	"syscall"

	"code.linenisgreat.com/chrest/go/src/bravo/config"
	"code.linenisgreat.com/chrest/go/src/bravo/server"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/stack_frame"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/ui"
)

func init() {
	log.SetPrefix("chrest-server ")
}

func main() {
	ctx := errors.MakeContextDefault()
	ctx.SetCancelOnSignals(syscall.SIGTERM)

	if err := ctx.Run(
		func(ctx errors.Context) {
			if err := run(ctx); err != nil {
				ctx.Cancel(err)
			}
		},
	); err != nil {
		var normalError stack_frame.ErrorStackTracer

		if errors.As(err, &normalError) && !normalError.ShouldShowStackTrace() {
			ui.Err().Printf("%s", normalError.Error())
		} else {
			if err != nil {
				ui.Err().Print(err)
			}
		}
	}
}

func run(ctx errors.Context) (err error) {
	defer ui.Err().Print("shut down complete")

	ui.Err().Printf("args: %q", os.Args)

	var c config.Config

	if c, err = config.Default(); err != nil {
		err = errors.Wrap(err)
		return
	}

	if err = c.Read(); err != nil {
		err = errors.Wrap(err)
		return err
	}

	srv := server.Server{
		ActiveContext: ctx,
	}

	srv.Initialize()
	srv.Serve()

	return err
}
