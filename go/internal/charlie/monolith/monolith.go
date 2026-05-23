// Package monolith wraps the monolith CLI (https://github.com/Y2Z/monolith).
// It accepts a rendered HTML DOM on stdin, inlines every asset as base64
// data: URIs, and returns a single self-contained HTML document.
package monolith

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// ErrMissing is returned when the monolith binary cannot be found on
// PATH. Callers (e.g. capturebatch) translate this into a
// `dependency-missing` per-capture error per RFC 0001.
var ErrMissing = errors.Errorf("monolith binary not found on PATH")

// Process pipes dom through `monolith -b <baseURL> -o - -q -M -` and
// returns the resulting self-contained HTML as a ReadCloser. Output is
// fully buffered: monolith's asset inlining requires it to read the
// whole document anyway, so streaming here would buy nothing.
//
// The `-M` (no-metadata) flag is set so the output is deterministic for
// a given DOM + asset snapshot; without it monolith appends a
// timestamp/source comment that would defeat content-addressed storage.
func Process(ctx context.Context, dom io.Reader, baseURL string) (io.ReadCloser, error) {
	if _, err := exec.LookPath("monolith"); err != nil {
		return nil, errors.Wrap(ErrMissing)
	}

	args := []string{"-o", "-", "-q", "-M"}
	if baseURL != "" {
		args = append(args, "-b", baseURL)
	}
	args = append(args, "-")

	cmd := exec.CommandContext(ctx, "monolith", args...)
	cmd.Stdin = dom
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("monolith exited non-zero: %s", msg)
	}

	return io.NopCloser(bytes.NewReader(stdout.Bytes())), nil
}
