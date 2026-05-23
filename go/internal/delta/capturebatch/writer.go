package capturebatch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
)

// WriterResult is the shape the writer protocol returns on stdout.
// RFC 0001 §Writer Protocol allows additional fields; we ignore them.
type WriterResult struct {
	ID   string `json:"id"`
	Size int64  `json:"size"`
}

// WriteThrough spawns the writer subprocess declared by cmd, streams
// src into its stdin until EOF, closes stdin, and parses the single
// JSON object the writer writes to stdout.
//
// Per RFC 0001 §Writer Protocol, the writer MUST exit 0 on success
// and MUST write exactly one line of JSON to stdout containing `id`
// and `size`. Non-zero exit or malformed stdout is a hard error; the
// caller maps it into a per-capture error.
func WriteThrough(ctx context.Context, cmd []string, src io.Reader) (WriterResult, error) {
	if len(cmd) == 0 {
		return WriterResult{}, fmt.Errorf("writer.cmd is empty")
	}

	c := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr

	stdin, err := c.StdinPipe()
	if err != nil {
		return WriterResult{}, fmt.Errorf("writer stdin pipe: %w", err)
	}

	if err := c.Start(); err != nil {
		return WriterResult{}, fmt.Errorf("writer start: %w", err)
	}

	// Stream src into the writer's stdin in a goroutine so the writer
	// can begin consuming while chrest is still producing.
	copyErrCh := make(chan error, 1)
	go func() {
		_, err := io.Copy(stdin, src)
		if closeErr := stdin.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
		copyErrCh <- err
	}()

	waitErr := c.Wait()
	copyErr := <-copyErrCh

	if waitErr != nil {
		return WriterResult{}, fmt.Errorf("writer exited %w (stderr: %q)", waitErr, stderr.String())
	}
	if copyErr != nil {
		return WriterResult{}, fmt.Errorf("writer stdin copy: %w", copyErr)
	}

	var result WriterResult
	if err := json.NewDecoder(&stdout).Decode(&result); err != nil {
		return WriterResult{}, fmt.Errorf("writer output parse: %w (raw: %q)", err, stdout.String())
	}
	if result.ID == "" {
		return WriterResult{}, fmt.Errorf("writer output missing id (raw: %q)", stdout.String())
	}
	return result, nil
}
