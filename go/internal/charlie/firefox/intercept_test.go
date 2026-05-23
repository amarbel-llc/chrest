//go:build spike

package firefox

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSession_AddResponseIntercept_ContinueResponse(t *testing.T) {
	if os.Getenv("CHREST_SPIKE_BIDI_INTERCEPT") != "1" {
		t.Skip("set CHREST_SPIKE_BIDI_INTERCEPT=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	s, err := NewSession(ctx)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	intercept, events, err := s.AddResponseIntercept(ctx, "https", "raw.githubusercontent.com")
	if err != nil {
		t.Fatalf("AddResponseIntercept: %v", err)
	}
	defer s.RemoveIntercept(ctx, intercept)

	url := "https://raw.githubusercontent.com/anthropics/anthropic-sdk-python/main/README.md"
	navDone := make(chan error, 1)
	go func() { navDone <- s.Navigate(ctx, url) }()

	select {
	case ev := <-events:
		if !ev.IsBlocked {
			t.Fatalf("expected isBlocked=true, got %+v", ev)
		}
		if ev.RequestID == "" {
			t.Fatalf("expected non-empty RequestID")
		}
		ct := ""
		for _, h := range ev.Headers {
			if strings.EqualFold(h.Name, "content-type") {
				ct = h.Value
				break
			}
		}
		if !strings.Contains(ct, "text/plain") {
			t.Fatalf("expected text/plain content-type; got %q", ct)
		}
		if err := s.ContinueResponse(ctx, ev.RequestID); err != nil {
			t.Fatalf("ContinueResponse: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("timeout waiting for intercept event")
	}

	if err := <-navDone; err != nil {
		t.Fatalf("Navigate: %v", err)
	}
}
