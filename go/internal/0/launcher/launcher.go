package launcher

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"syscall"
	"time"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

type DiscoveryKind int

const (
	StderrRegex DiscoveryKind = iota
	HTTPEndpoint
)

type DiscoveryStrategy struct {
	Kind    DiscoveryKind
	Pattern *regexp.Regexp
}

type BrowserConfig struct {
	BinaryNames []string
	Args        []string
	URL         string
	Discovery   DiscoveryStrategy
	TempProfile string
	// ProfilePath, when set, takes precedence over TempProfile: the
	// browser is launched against this exact directory and the launcher
	// does not clean it up.
	ProfilePath string
}

type Process struct {
	cmd     *exec.Cmd
	wsURL   string
	cleanup func()
	// args is the full argv passed to the browser (minus argv[0]).
	// Recorded so capture-batch can emit it in the spec artifact's
	// browser.command_line field.
	args []string
	// exited receives the result of cmd.Wait() exactly once and is then
	// closed. A dedicated waiter goroutine in Launch owns the actual
	// cmd.Wait call, so callers must read this channel rather than calling
	// Wait themselves.
	exited chan error
}

// Args returns the argv (excluding argv[0]) the browser was launched with.
func (p *Process) Args() []string {
	return p.args
}

func (p *Process) WSURL() string {
	return p.wsURL
}

// Exited returns a channel that delivers the cmd.Wait() error when the
// browser process exits, then closes. Consumers can select on it to detect
// an unexpected mid-session crash.
func (p *Process) Exited() <-chan error {
	return p.exited
}

func (p *Process) Close() error {
	var firstErr error

	if p.cmd.Process != nil {
		killProcessTree(p.cmd.Process.Pid)
		err := <-p.exited
		if err != nil {
			if _, ok := err.(*exec.ExitError); !ok {
				firstErr = err
			}
		}
	}

	if p.cleanup != nil {
		p.cleanup()
	}

	return firstErr
}

// killProcessTree SIGKILLs the root process and every descendant.
//
// Two strategies, belt-and-suspenders:
//  1. collectDescendants enumerates the descendant tree (platform-specific
//     — /proc on Linux, sysctl kern.proc.all on Darwin) and SIGKILLs each,
//     catching Firefox content processes that detach into their own
//     process groups and would escape a bare -pgid kill.
//  2. SIGKILL the root's process group, picking up anything the walk
//     missed due to reparenting races.
//
// The root is stopped first so it cannot spawn new children during the walk.
// On a normal system, orphaned helpers would be reaped by init; inside
// bwrap --unshare-pid sandboxes there is no init, so unkilled descendants
// linger forever and block the sandbox from exiting.
func killProcessTree(rootPid int) {
	_ = syscall.Kill(rootPid, syscall.SIGSTOP)

	for _, pid := range collectDescendants(rootPid) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}

	if pgid, err := syscall.Getpgid(rootPid); err == nil {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}

	_ = syscall.Kill(rootPid, syscall.SIGKILL)
}

func findBinary(names []string) (string, error) {
	for _, name := range names {
		if filepath.IsAbs(name) {
			if _, err := os.Stat(name); err == nil {
				return name, nil
			}
			continue
		}
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", errors.Errorf("no browser binary found (tried %v)", names)
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, errors.Wrap(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port, nil
}

func Launch(ctx context.Context, cfg BrowserConfig) (*Process, error) {
	binaryPath, err := findBinary(cfg.BinaryNames)
	if err != nil {
		return nil, err
	}

	args := append([]string{}, cfg.Args...)
	var cleanup func()

	switch {
	case cfg.ProfilePath != "":
		args = append(args, "--profile", cfg.ProfilePath)
		// No cleanup — caller owns the directory.
	case cfg.TempProfile != "":
		dir, err := os.MkdirTemp("", cfg.TempProfile+"*")
		if err != nil {
			return nil, errors.Wrap(err)
		}
		args = append(args, "--profile", dir)
		cleanup = func() {
			os.RemoveAll(dir)
		}
	}

	var port int
	if cfg.Discovery.Kind == HTTPEndpoint {
		port, err = freePort()
		if err != nil {
			if cleanup != nil {
				cleanup()
			}
			return nil, errors.Wrap(err)
		}
		args = append(
			args,
			fmt.Sprintf("--remote-debugging-port=%d", port),
			"--remote-allow-origins=*",
		)
	}

	args = append(args, cfg.URL)

	cmd := exec.CommandContext(ctx, binaryPath, args...)
	// New process group so we can SIGKILL the whole tree in Close.
	// Without this, browser helper processes (content, tab, utility, ...)
	// become orphans when we kill the parent and leak until init reaps
	// them — which never happens inside bwrap --unshare-pid sandboxes.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Disconnect the browser's stdin/stdout from our fds. If we inherit
	// them, the browser forks content processes that also inherit them
	// — including a pipe write-end when chrest is invoked via bash's
	// `run` / command substitution. That pipe never sees EOF until the
	// last content-process dies, which causes the bats harness to hang
	// on shutdown even after every test passes. stderr stays on a pipe
	// because the launcher scrapes it for the WebSocket URL.
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		if cleanup != nil {
			cleanup()
		}
		return nil, errors.Wrap(err)
	}
	defer devNull.Close()
	cmd.Stdin = devNull
	cmd.Stdout = devNull

	log.Printf("launching: %s %v", binaryPath, args)

	stderr, err := cmd.StderrPipe()
	if err != nil {
		if cleanup != nil {
			cleanup()
		}
		return nil, errors.Wrap(err)
	}

	if err := cmd.Start(); err != nil {
		if cleanup != nil {
			cleanup()
		}
		return nil, errors.Wrap(err)
	}
	pid := cmd.Process.Pid
	log.Printf("browser started (pid %d)", pid)

	exited := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		logBrowserExit(pid, err)
		exited <- err
		close(exited)
	}()

	killAndCleanup := func() {
		killProcessTree(pid)
		<-exited
		if cleanup != nil {
			cleanup()
		}
	}

	if cfg.Discovery.Kind == HTTPEndpoint {
		go drainStderr(stderr)
	}

	var wsURL string

	switch cfg.Discovery.Kind {
	case StderrRegex:
		wsURL, err = discoverStderr(ctx, stderr, cfg.Discovery.Pattern)
	case HTTPEndpoint:
		wsURL, err = discoverHTTP(ctx, port)
	}

	if err != nil {
		killAndCleanup()
		return nil, err
	}

	log.Printf("browser WebSocket URL: %s", wsURL)
	return &Process{cmd: cmd, wsURL: wsURL, cleanup: cleanup, args: args, exited: exited}, nil
}

func drainStderr(stderr io.Reader) {
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		log.Printf("browser stderr: %s", scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		log.Printf("browser stderr scanner error: %v", err)
	}
}

func logBrowserExit(pid int, err error) {
	if err == nil {
		log.Printf("browser exited cleanly (pid %d)", pid)
		return
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		log.Printf("browser wait error (pid %d): %v", pid, err)
		return
	}
	if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok {
		switch {
		case ws.Signaled():
			log.Printf("browser killed by signal %v (pid %d)", ws.Signal(), pid)
		case ws.Exited():
			log.Printf("browser exited with code %d (pid %d)", ws.ExitStatus(), pid)
		default:
			log.Printf("browser exited (pid %d, status %v)", pid, ws)
		}
		return
	}
	log.Printf("browser exited with error (pid %d): %v", pid, err)
}

func discoverStderr(ctx context.Context, stderr io.Reader, pattern *regexp.Regexp) (string, error) {
	found := make(chan string, 1)
	done := make(chan error, 1)

	go func() {
		scanner := bufio.NewScanner(stderr)
		var sentURL bool
		for scanner.Scan() {
			line := scanner.Text()
			log.Printf("browser stderr: %s", line)
			if !sentURL {
				if m := pattern.FindStringSubmatch(line); len(m) > 1 {
					found <- m[1]
					sentURL = true
				}
			}
		}
		if err := scanner.Err(); err != nil {
			log.Printf("browser stderr scanner error: %v", err)
			if !sentURL {
				done <- err
				return
			}
		}
		if !sentURL {
			done <- errors.Errorf("browser stderr closed without emitting WebSocket URL")
		}
	}()

	select {
	case wsURL := <-found:
		return wsURL, nil
	case err := <-done:
		return "", errors.Wrapf(err, "browser exited before emitting WebSocket URL")
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func discoverHTTP(ctx context.Context, port int) (string, error) {
	url := fmt.Sprintf("http://127.0.0.1:%d/json/list", port)
	client := &http.Client{Timeout: 2 * time.Second}
	delay := 50 * time.Millisecond
	for {
		if wsURL, found := pollDevToolsEndpoint(client, url); found {
			return wsURL, nil
		}

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(delay):
			if delay < 500*time.Millisecond {
				delay *= 2
			}
		}
	}
}

func pollDevToolsEndpoint(client *http.Client, url string) (string, bool) {
	resp, err := client.Get(url)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()

	var entries []struct {
		Type                 string `json:"type"`
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return "", false
	}

	for _, e := range entries {
		if e.Type == "page" && e.WebSocketDebuggerURL != "" {
			return e.WebSocketDebuggerURL, true
		}
	}
	return "", false
}
