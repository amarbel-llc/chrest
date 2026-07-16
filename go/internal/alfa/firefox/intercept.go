package firefox

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"strings"

	"code.linenisgreat.com/chrest/go/internal/0/bidi"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// BiDiDebug reports whether CHREST_BIDI_DEBUG is set, gating the
// per-event tracing added to debug real-world navigate-timeout
// hangs (e.g. chrest/eng#251) that don't reproduce against the
// hermetic subresource-overflow fixtures.
func BiDiDebug() bool {
	return os.Getenv("CHREST_BIDI_DEBUG") != ""
}

// InterceptedResponse is delivered to the channel returned by
// AddResponseIntercept whenever a top-level response matching the
// pattern is paused at the responseStarted phase. The caller MUST
// invoke either Session.ContinueResponse or Session.FailRequest
// with RequestID before the in-flight Navigate can return.
type InterceptedResponse struct {
	Navigation string
	RequestID  string
	IsBlocked  bool
	URL        string
	Status     int
	Headers    []HTTPHeader
	Intercepts []string
}

type interceptedResponseEvent struct {
	Context    string   `json:"context"`
	Navigation string   `json:"navigation"`
	IsBlocked  bool     `json:"isBlocked"`
	Intercepts []string `json:"intercepts"`
	Request    struct {
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

// AddResponseIntercept registers a network.responseStarted intercept
// scoped to this session's browsing context and the given URL pattern,
// and returns the intercept id plus a channel that receives intercept
// events. The channel is closed when RemoveIntercept is called.
func (s *Session) AddResponseIntercept(ctx context.Context, protocol, hostname string) (string, <-chan InterceptedResponse, error) {
	sub := s.conn.SubscribeWithFilter(
		[]string{"network.responseStarted"},
		func(ev bidi.EventFrame) bool {
			var peek interceptedResponseEvent
			if err := json.Unmarshal(ev.Params, &peek); err != nil {
				return false
			}
			// Deliver every blocked response in this context. The
			// consumer is responsible for distinguishing the top-level
			// navigation event (Navigation != "") from subresource
			// events and continuing each one — see the dispatcher
			// loop in cmd/chrest/main.go. Filtering out subresources
			// here would leave them paused at the BiDi server, the
			// page would never reach the load event, and Navigate
			// would deadlock until its 30s timeout.
			return peek.Context == s.contextID && peek.IsBlocked
		},
	)

	if _, err := s.conn.Send("session.subscribe", map[string]any{
		"events":   []string{"network.responseStarted"},
		"contexts": []string{s.contextID},
	}); err != nil {
		sub.Close()
		return "", nil, errors.Wrap(err)
	}

	// When protocol+hostname are both supplied, scope the intercept to
	// that origin. When either is empty (e.g. the dispatcher needs to
	// follow cross-host redirects, or the URL has no hostname), omit
	// urlPatterns so the intercept matches every response in this
	// browsing context — context scoping alone is sufficient because
	// the session is fresh per fetch.
	addParams := map[string]any{
		"phases":   []string{"responseStarted"},
		"contexts": []string{s.contextID},
	}
	if protocol != "" && hostname != "" {
		addParams["urlPatterns"] = []map[string]any{
			{"type": "pattern", "protocol": protocol, "hostname": hostname},
		}
	}
	result, err := s.conn.Send("network.addIntercept", addParams)
	if err != nil {
		sub.Close()
		return "", nil, errors.Wrap(err)
	}
	var added struct {
		Intercept string `json:"intercept"`
	}
	if err := json.Unmarshal(result, &added); err != nil {
		sub.Close()
		return "", nil, errors.Wrap(err)
	}

	// Buffer matches `subBuffer` in bravo/bidi/protocol.go so the
	// producer can drain the broker as fast as the broker can fill
	// it. Overflow still falls through to the producer's continue-on-
	// drop path (chrest#66), but a deeper buffer means we don't pay
	// that latency on every burst.
	out := make(chan InterceptedResponse, 64)
	go func() {
		defer close(out)
		for ev := range sub.Events {
			var decoded interceptedResponseEvent
			if err := json.Unmarshal(ev.Params, &decoded); err != nil {
				continue
			}
			headers := make([]HTTPHeader, 0, len(decoded.Response.Headers))
			for _, h := range decoded.Response.Headers {
				headers = append(headers, HTTPHeader{Name: h.Name, Value: h.Value.Value})
			}
			// Send the typed event to the caller. If the caller has stopped
			// reading or can't keep up with the burst, drop rather than block.
			// On drop we MUST release the paused request ourselves —
			// `IsBlocked` events represent a request held at the BiDi server
			// at responseStarted, and abandoning one without ContinueResponse
			// (or FailRequest) leaves it paused indefinitely. That keeps the
			// page from reaching `load` and makes Navigate's wait:complete
			// deadlock until the BiDi RPC times out at 30s.
			if BiDiDebug() {
				log.Printf("bidi-debug: intercept event nav=%q blocked=%v status=%d url=%s",
					decoded.Navigation, decoded.IsBlocked, decoded.Response.Status, decoded.Response.URL)
			}

			select {
			case out <- InterceptedResponse{
				Navigation: decoded.Navigation,
				RequestID:  decoded.Request.Request,
				IsBlocked:  decoded.IsBlocked,
				URL:        decoded.Response.URL,
				Status:     decoded.Response.Status,
				Headers:    headers,
				Intercepts: decoded.Intercepts,
			}:
			case <-ctx.Done():
				return
			default:
				// Caller can't keep up. Release the paused request inline so
				// the browser can continue loading, then drop the event.
				// Errors are ignored — by definition we have no caller to
				// surface them to, and the connection death case is handled
				// by the next `for ev := range sub.Events` iteration falling
				// off the closed channel.
				if BiDiDebug() {
					log.Printf("bidi-debug: intercept buffer full, dropping+releasing nav=%q url=%s",
						decoded.Navigation, decoded.Response.URL)
				}
				if decoded.IsBlocked {
					_ = s.ContinueResponse(ctx, decoded.Request.Request)
				}
			}
		}
	}()

	// Stash the subscription on the session so RemoveIntercept can close it.
	s.intercepts.Store(added.Intercept, sub)

	return added.Intercept, out, nil
}

// ContinueResponse releases a paused request, allowing the response
// to be delivered and Navigate to complete.
func (s *Session) ContinueResponse(ctx context.Context, requestID string) error {
	_, err := s.conn.Send("network.continueResponse", map[string]any{
		"request": requestID,
	})
	return errors.Wrap(err)
}

// FailRequest aborts a paused request. The corresponding Navigate
// call returns a BiDi error wrapping NS_ERROR_ABORT; callers in the
// HTTPError / Binary branches must recognise and swallow that
// specific error.
func (s *Session) FailRequest(ctx context.Context, requestID string) error {
	_, err := s.conn.Send("network.failRequest", map[string]any{
		"request": requestID,
	})
	return errors.Wrap(err)
}

// RemoveIntercept removes a previously-registered intercept and
// closes its event channel.
func (s *Session) RemoveIntercept(ctx context.Context, interceptID string) error {
	if v, ok := s.intercepts.LoadAndDelete(interceptID); ok {
		v.(*bidi.Subscription).Close()
	}
	_, err := s.conn.Send("network.removeIntercept", map[string]any{
		"intercept": interceptID,
	})
	return errors.Wrap(err)
}

// IsAbortedNavigation reports whether err is the BiDi error returned
// by Navigate after an explicit FailRequest.
func IsAbortedNavigation(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "NS_ERROR_ABORT")
}
