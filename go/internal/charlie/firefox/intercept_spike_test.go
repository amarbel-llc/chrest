//go:build spike

// Spike: verify that Firefox's WebDriver BiDi implementation supports
// network.addIntercept at the responseStarted phase, plus
// network.continueResponse and network.failRequest.
//
// Background: docs/plans/2026-04-29-web-fetch-content-type-dispatch-design.md
//
// Run with:
//
//	just explore-bidi-intercept
//
// Output goes to stdout; not asserted — the human reads the trace.
package firefox

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSpikeBiDiResponseIntercept(t *testing.T) {
	if os.Getenv("CHREST_SPIKE_BIDI_INTERCEPT") != "1" {
		t.Skip("set CHREST_SPIKE_BIDI_INTERCEPT=1 to run this spike")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	s, err := NewSession(ctx)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	t.Logf("session up; browser=%s/%s context=%s",
		s.capabilities.BrowserName, s.capabilities.BrowserVersion, s.contextID)

	// Subscribe locally to network.responseStarted before we ask the
	// server to emit it. Same pattern used by initSession for
	// responseCompleted.
	startedSub := s.conn.Subscribe([]string{"network.responseStarted"})
	defer startedSub.Close()

	if _, err := s.conn.Send("session.subscribe", map[string]any{
		"events":   []string{"network.responseStarted"},
		"contexts": []string{s.contextID},
	}); err != nil {
		t.Fatalf("session.subscribe(network.responseStarted): %v", err)
	}
	t.Logf("subscribed to network.responseStarted")

	// Try to register an intercept at the responseStarted phase.
	interceptResult, err := s.conn.Send("network.addIntercept", map[string]any{
		"phases":   []string{"responseStarted"},
		"contexts": []string{s.contextID},
		"urlPatterns": []map[string]any{
			{"type": "pattern", "protocol": "https", "hostname": "raw.githubusercontent.com"},
		},
	})
	if err != nil {
		t.Fatalf("network.addIntercept: %v", err)
	}
	var added struct {
		Intercept string `json:"intercept"`
	}
	if err := json.Unmarshal(interceptResult, &added); err != nil {
		t.Fatalf("unmarshal addIntercept result: %v\nraw=%s", err, string(interceptResult))
	}
	t.Logf("addIntercept ok: id=%s", added.Intercept)

	// Pick a known URL — the SDK README we exercised manually earlier.
	url := "https://raw.githubusercontent.com/anthropics/anthropic-sdk-python/main/README.md"

	// Kick navigate in a goroutine — Navigate blocks on `wait: complete`,
	// which won't fire until we either continueResponse or failRequest.
	navDone := make(chan error, 1)
	go func() {
		navDone <- s.Navigate(ctx, url)
	}()

	// Wait for the responseStarted event for our top-level navigation.
	deadline := time.After(20 * time.Second)
	var intercepted struct {
		Context     string   `json:"context"`
		Navigation  string   `json:"navigation"`
		IsBlocked   bool     `json:"isBlocked"`
		Intercepts  []string `json:"intercepts"`
		Request     struct {
			Request string `json:"request"`
		} `json:"request"`
		Response struct {
			URL     string `json:"url"`
			Status  int    `json:"status"`
			Headers []struct {
				Name  string `json:"name"`
				Value struct {
					Value string `json:"value"`
				} `json:"value"`
			} `json:"headers"`
		} `json:"response"`
	}
	for {
		select {
		case ev, ok := <-startedSub.Events:
			if !ok {
				t.Fatalf("subscription closed before intercept fired")
			}
			if err := json.Unmarshal(ev.Params, &intercepted); err != nil {
				t.Logf("unparsable responseStarted: %v", err)
				continue
			}
			if intercepted.Context != s.contextID || intercepted.Navigation == "" {
				continue
			}
			t.Logf("responseStarted: nav=%s url=%s status=%d isBlocked=%v intercepts=%v request=%s",
				intercepted.Navigation, intercepted.Response.URL, intercepted.Response.Status,
				intercepted.IsBlocked, intercepted.Intercepts, intercepted.Request.Request)
			ct := ""
			for _, h := range intercepted.Response.Headers {
				if strings.EqualFold(h.Name, "content-type") {
					ct = h.Value.Value
					break
				}
			}
			t.Logf("response Content-Type: %q", ct)
			goto Decide
		case <-deadline:
			t.Fatalf("timed out waiting for responseStarted event")
		case <-ctx.Done():
			t.Fatalf("ctx done: %v", ctx.Err())
		}
	}

Decide:
	if !intercepted.IsBlocked {
		t.Fatalf("event not blocked — intercept did not catch this navigation")
	}

	// Try to release the response.
	t.Logf("calling network.continueResponse(request=%s)", intercepted.Request.Request)
	if _, err := s.conn.Send("network.continueResponse", map[string]any{
		"request": intercepted.Request.Request,
	}); err != nil {
		t.Fatalf("network.continueResponse: %v", err)
	}
	t.Logf("continueResponse ok")

	// Wait for navigate to settle.
	select {
	case err := <-navDone:
		if err != nil {
			t.Fatalf("Navigate after continueResponse: %v", err)
		}
		t.Logf("Navigate completed after continueResponse")
	case <-time.After(20 * time.Second):
		t.Fatalf("Navigate did not return after continueResponse")
	}

	// Now try the failRequest path on a fresh navigation.
	t.Logf("\n--- second navigation: failRequest path ---")
	navDone2 := make(chan error, 1)
	go func() {
		navDone2 <- s.Navigate(ctx, url+"?second=1")
	}()
	deadline = time.After(20 * time.Second)
	var second struct {
		Context    string `json:"context"`
		Navigation string `json:"navigation"`
		IsBlocked  bool   `json:"isBlocked"`
		Request    struct {
			Request string `json:"request"`
		} `json:"request"`
	}
	for {
		select {
		case ev, ok := <-startedSub.Events:
			if !ok {
				t.Fatalf("subscription closed during second nav")
			}
			if err := json.Unmarshal(ev.Params, &second); err != nil {
				continue
			}
			if second.Context != s.contextID || second.Navigation == "" || !second.IsBlocked {
				continue
			}
			goto Fail
		case <-deadline:
			t.Fatalf("second nav: timed out waiting for intercept")
		}
	}
Fail:
	t.Logf("second responseStarted: request=%s isBlocked=%v", second.Request.Request, second.IsBlocked)
	if _, err := s.conn.Send("network.failRequest", map[string]any{
		"request": second.Request.Request,
	}); err != nil {
		t.Fatalf("network.failRequest: %v", err)
	}
	t.Logf("failRequest ok")

	// Navigate should resolve with an error after the request is failed.
	select {
	case err := <-navDone2:
		t.Logf("Navigate after failRequest returned err=%v", err)
		// We expect a non-nil err — Firefox typically reports
		// NS_BINDING_ABORTED. The point of the spike is just to confirm
		// failRequest does in fact terminate the navigation cleanly.
	case <-time.After(20 * time.Second):
		t.Fatalf("Navigate did not return after failRequest")
	}

	// Cleanup the intercept.
	if _, err := s.conn.Send("network.removeIntercept", map[string]any{
		"intercept": added.Intercept,
	}); err != nil {
		t.Logf("removeIntercept (cleanup) returned err: %v", err)
	}

	fmt.Println("\nSPIKE PASSED — BiDi response interception works")
}
