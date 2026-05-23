package bidi

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// frame covers both request/response and event shapes that can appear
// on the wire. The readLoop discriminates on presence of `id`: response
// frames always carry the id that matched the originating request,
// event frames omit it and carry `method` + `params`.
type frame struct {
	Type      string          `json:"type,omitempty"`
	ID        int64           `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	ErrorType string          `json:"error,omitempty"`
	Message   string          `json:"message,omitempty"`
}

type request struct {
	ID     int64           `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// EventFrame is delivered to Subscribe channels for each matching
// incoming event. Params is raw JSON so callers can decode it into
// any event-specific struct.
type EventFrame struct {
	Method string
	Params json.RawMessage
}

const (
	opcodeText  = 0x1
	opcodeClose = 0x8
	opcodePing  = 0x9
	opcodePong  = 0xA

	wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

	dialTimeout = 5 * time.Second

	// Bound the per-call wait so Send cannot block forever if the server
	// never responds. Previously enforced via conn.SetReadDeadline around
	// the inline read loop; now enforced by per-call timer in Send.
	sendTimeout = 30 * time.Second

	// Per-subscriber buffer. Events drop with a warning if a slow
	// subscriber lets its channel fill — we will not block the read
	// loop or the rest of the subscribers.
	subBuffer = 64
)

// EventFilter is a predicate applied before buffering an event into a
// subscriber's channel. Returning false drops the event before it
// reaches the channel, preventing buffer overflow from irrelevant
// high-volume events.
type EventFilter func(EventFrame) bool

type subscriber struct {
	ch     chan EventFrame
	filter EventFilter // nil = accept all
}

// Conn wraps a raw TCP connection with WebSocket framing for BiDi
// JSON-RPC, plus a background read loop that demuxes response frames
// to pending request callers and event frames to subscribers.
type Conn struct {
	conn net.Conn
	br   *bufio.Reader

	writeMu sync.Mutex // serialises outbound WS frames
	seq     atomic.Int64

	mu      sync.Mutex
	pending map[int64]chan frame
	subs    map[string]map[int64]subscriber
	subSeq  atomic.Int64

	done    chan struct{}
	connErr atomic.Value // error, set once when the read loop exits
}

// Dial connects to a BiDi WebSocket endpoint.
// The baseURL is the URL Firefox logs (e.g. ws://127.0.0.1:PORT).
// Firefox requires the /session path for BiDi connections.
func Dial(baseURL string) (*Conn, error) {
	wsURL := strings.TrimRight(baseURL, "/") + "/session"
	log.Printf("bidi: dialing %s", wsURL)

	host := strings.TrimPrefix(wsURL, "ws://")
	pathIdx := strings.Index(host, "/")
	hostPort := host
	path := "/"
	if pathIdx >= 0 {
		hostPort = host[:pathIdx]
		path = host[pathIdx:]
	}

	conn, err := net.DialTimeout("tcp", hostPort, dialTimeout)
	if err != nil {
		return nil, fmt.Errorf("bidi tcp dial: %w", err)
	}

	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		conn.Close()
		return nil, fmt.Errorf("bidi key gen: %w", err)
	}
	wsKey := base64.StdEncoding.EncodeToString(keyBytes)

	upgradeReq := fmt.Sprintf(
		"GET %s HTTP/1.1\r\n"+
			"Host: %s\r\n"+
			"Upgrade: websocket\r\n"+
			"Connection: Upgrade\r\n"+
			"Sec-WebSocket-Key: %s\r\n"+
			"Sec-WebSocket-Version: 13\r\n"+
			"\r\n",
		path, hostPort, wsKey,
	)

	if _, err := conn.Write([]byte(upgradeReq)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("bidi upgrade write: %w", err)
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("bidi upgrade read: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode != 101 {
		conn.Close()
		return nil, fmt.Errorf("bidi upgrade failed: HTTP %d %s", resp.StatusCode, resp.Status)
	}

	// Validate Sec-WebSocket-Accept per RFC 6455 section 4.1.
	expectedAccept := computeAcceptKey(wsKey)
	if got := resp.Header.Get("Sec-WebSocket-Accept"); got != expectedAccept {
		conn.Close()
		return nil, fmt.Errorf("bidi: invalid Sec-WebSocket-Accept: got %q, want %q", got, expectedAccept)
	}

	log.Printf("bidi: connected")

	c := &Conn{
		conn:    conn,
		br:      br,
		pending: make(map[int64]chan frame),
		subs:    make(map[string]map[int64]subscriber),
		done:    make(chan struct{}),
	}
	go c.readLoop()
	return c, nil
}

// computeAcceptKey computes the expected Sec-WebSocket-Accept value.
func computeAcceptKey(key string) string {
	h := sha1.New()
	h.Write([]byte(key + wsGUID))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// writeFrame writes a WebSocket text frame with masking (client must mask).
// Callers must hold c.writeMu.
func (c *Conn) writeFrame(payload []byte) error {
	// Text frame, FIN bit set.
	header := []byte{0x80 | opcodeText}

	// Payload length with mask bit set.
	length := len(payload)
	switch {
	case length <= 125:
		header = append(header, byte(length)|0x80)
	case length <= 65535:
		header = append(header, 126|0x80, byte(length>>8), byte(length))
	default:
		buf := make([]byte, 8)
		binary.BigEndian.PutUint64(buf, uint64(length))
		header = append(header, 127|0x80)
		header = append(header, buf...)
	}

	// Masking key.
	mask := make([]byte, 4)
	if _, err := rand.Read(mask); err != nil {
		return err
	}
	header = append(header, mask...)

	// Mask the payload.
	masked := make([]byte, len(payload))
	for i, b := range payload {
		masked[i] = b ^ mask[i%4]
	}

	if _, err := c.conn.Write(header); err != nil {
		return err
	}
	_, err := c.conn.Write(masked)
	return err
}

// writePong sends a WebSocket pong frame with the given payload.
// Callers must hold c.writeMu.
func (c *Conn) writePong(payload []byte) error {
	header := []byte{0x80 | opcodePong}

	length := len(payload)
	if length > 125 {
		length = 125
	}
	header = append(header, byte(length)|0x80)

	mask := make([]byte, 4)
	if _, err := rand.Read(mask); err != nil {
		return err
	}
	header = append(header, mask...)

	masked := make([]byte, length)
	for i := 0; i < length; i++ {
		masked[i] = payload[i] ^ mask[i%4]
	}

	if _, err := c.conn.Write(header); err != nil {
		return err
	}
	_, err := c.conn.Write(masked)
	return err
}

// readFrame reads a WebSocket frame, handling control frames (close, ping).
// Returns the payload for data frames only. Must only be called from readLoop.
func (c *Conn) readFrame() ([]byte, error) {
	for {
		hdr := make([]byte, 2)
		if _, err := io.ReadFull(c.br, hdr); err != nil {
			return nil, err
		}

		opcode := hdr[0] & 0x0F
		masked := hdr[1]&0x80 != 0
		length := uint64(hdr[1] & 0x7F)

		switch length {
		case 126:
			buf := make([]byte, 2)
			if _, err := io.ReadFull(c.br, buf); err != nil {
				return nil, err
			}
			length = uint64(binary.BigEndian.Uint16(buf))
		case 127:
			buf := make([]byte, 8)
			if _, err := io.ReadFull(c.br, buf); err != nil {
				return nil, err
			}
			length = binary.BigEndian.Uint64(buf)
		}

		var mask []byte
		if masked {
			mask = make([]byte, 4)
			if _, err := io.ReadFull(c.br, mask); err != nil {
				return nil, err
			}
		}

		payload := make([]byte, length)
		if _, err := io.ReadFull(c.br, payload); err != nil {
			return nil, err
		}

		if masked {
			for i := range payload {
				payload[i] ^= mask[i%4]
			}
		}

		switch opcode {
		case opcodeClose:
			return nil, fmt.Errorf("bidi: server sent close frame")
		case opcodePing:
			c.writeMu.Lock()
			err := c.writePong(payload)
			c.writeMu.Unlock()
			if err != nil {
				return nil, fmt.Errorf("bidi: pong failed: %w", err)
			}
			continue
		case opcodePong:
			continue
		default:
			return payload, nil
		}
	}
}

// readLoop owns all reads on the connection. It demuxes incoming
// frames to pending request callers (by id) or subscribers (by method).
// Exits when readFrame returns an error; records the cause in connErr
// and closes done so Send/Subscribe callers can unblock.
func (c *Conn) readLoop() {
	defer close(c.done)
	for {
		payload, err := c.readFrame()
		if err != nil {
			c.connErr.Store(err)
			c.failPending(err)
			c.closeSubs()
			return
		}
		var f frame
		if err := json.Unmarshal(payload, &f); err != nil {
			log.Printf("bidi: discarding unparsable frame: %v", err)
			continue
		}

		// Response frames carry the request id we assigned via seq.Add,
		// so they are always non-zero. Event frames omit id entirely
		// (unmarshals to zero value).
		if f.ID != 0 {
			c.mu.Lock()
			ch, ok := c.pending[f.ID]
			if ok {
				delete(c.pending, f.ID)
			}
			c.mu.Unlock()
			if !ok {
				log.Printf("bidi: orphaned response for id=%d method=%s", f.ID, f.Method)
				continue
			}
			ch <- f
			continue
		}

		if f.Method == "" {
			log.Printf("bidi: discarding frame with no id and no method: %s", string(payload))
			continue
		}

		c.dispatchEvent(f.Method, EventFrame{Method: f.Method, Params: f.Params})
	}
}

func (c *Conn) failPending(err error) {
	c.mu.Lock()
	pending := c.pending
	c.pending = make(map[int64]chan frame)
	c.mu.Unlock()
	for _, ch := range pending {
		select {
		case ch <- frame{Type: "error", ErrorType: "connection-closed", Message: err.Error()}:
		default:
		}
	}
}

func (c *Conn) closeSubs() {
	c.mu.Lock()
	subs := c.subs
	c.subs = make(map[string]map[int64]subscriber)
	c.mu.Unlock()
	for _, byID := range subs {
		for _, s := range byID {
			close(s.ch)
		}
	}
}

// dispatchEvent fans out a single event to every subscriber for its
// method. Subscriber filters are applied before buffering — events
// rejected by a filter never touch the channel. Slow subscribers whose
// channels are full lose events with a warning; the read loop never
// blocks on a single consumer.
func (c *Conn) dispatchEvent(method string, ev EventFrame) {
	c.mu.Lock()
	matches := c.subs[method]
	targets := make([]subscriber, 0, len(matches))
	for _, s := range matches {
		targets = append(targets, s)
	}
	c.mu.Unlock()
	for _, s := range targets {
		if s.filter != nil && !s.filter(ev) {
			continue
		}
		select {
		case s.ch <- ev:
		default:
			log.Printf("bidi: subscriber channel full for %s, dropping event", method)
		}
	}
}

// Send sends a BiDi method call and blocks until the matching response
// arrives, the per-call deadline expires, or the connection fails.
func (c *Conn) Send(method string, params any) (json.RawMessage, error) {
	if err := c.err(); err != nil {
		return nil, fmt.Errorf("bidi send: %w", err)
	}

	id := c.seq.Add(1)

	var rawParams json.RawMessage
	if params != nil {
		var err error
		if rawParams, err = json.Marshal(params); err != nil {
			return nil, fmt.Errorf("bidi marshal params: %w", err)
		}
	}

	req := request{ID: id, Method: method, Params: rawParams}
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("bidi marshal request: %w", err)
	}

	// Register the response channel before writing, so the reader
	// cannot deliver into a missing slot.
	resCh := make(chan frame, 1)
	c.mu.Lock()
	c.pending[id] = resCh
	c.mu.Unlock()

	c.writeMu.Lock()
	err = c.writeFrame(payload)
	c.writeMu.Unlock()
	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("bidi send: %w", err)
	}

	timer := time.NewTimer(sendTimeout)
	defer timer.Stop()

	select {
	case resp := <-resCh:
		if resp.Type == "error" || resp.ErrorType != "" {
			return nil, fmt.Errorf("bidi error %s: %s", resp.ErrorType, resp.Message)
		}
		return resp.Result, nil
	case <-c.done:
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("bidi send: %w", c.err())
	case <-timer.C:
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("bidi: timed out after %s waiting for response to %s (id=%d)", sendTimeout, method, id)
	}
}

// Subscription holds a channel of events for the methods it was
// registered against. Close the subscription when done to release
// server-side subscriptions (via session.unsubscribe is the caller's
// responsibility) and remove the local channel from the dispatch map.
type Subscription struct {
	Events  <-chan EventFrame
	conn    *Conn
	methods []string
	id      int64
	once    sync.Once
}

// Close removes the subscription from the dispatch map. Idempotent.
// Does not call session.unsubscribe — that is left to the caller
// since only they know the BiDi subscription id(s).
func (s *Subscription) Close() {
	s.once.Do(func() {
		s.conn.mu.Lock()
		defer s.conn.mu.Unlock()
		for _, m := range s.methods {
			if byID := s.conn.subs[m]; byID != nil {
				if sub, ok := byID[s.id]; ok {
					delete(byID, s.id)
					close(sub.ch)
				}
				if len(byID) == 0 {
					delete(s.conn.subs, m)
				}
			}
		}
	})
}

// Subscribe registers a local event channel for the given methods and
// returns a Subscription whose Events channel delivers every matching
// event until Close is called or the connection dies.
//
// The channel is buffered; slow consumers drop events rather than
// block the read loop. Callers that need every event must drain the
// channel promptly.
//
// Subscribe does NOT call session.subscribe on the remote peer — that
// is left to the caller because BiDi subscriptions can be scoped by
// context and have their own id semantics. Typical flow:
//
//	sub := conn.Subscribe([]string{"network.responseCompleted"})
//	defer sub.Close()
//	_, err := conn.Send("session.subscribe", map[string]any{"events": []string{"network.responseCompleted"}})
func (c *Conn) Subscribe(methods []string) *Subscription {
	return c.SubscribeWithFilter(methods, nil)
}

// SubscribeWithFilter is like Subscribe but applies filter before
// buffering events. Events for which filter returns false are silently
// discarded in the dispatch loop, never consuming channel capacity.
// Pass nil for an unfiltered subscription (same as Subscribe).
func (c *Conn) SubscribeWithFilter(methods []string, filter EventFilter) *Subscription {
	ch := make(chan EventFrame, subBuffer)
	id := c.subSeq.Add(1)
	sub := subscriber{ch: ch, filter: filter}
	c.mu.Lock()
	for _, m := range methods {
		byID := c.subs[m]
		if byID == nil {
			byID = make(map[int64]subscriber)
			c.subs[m] = byID
		}
		byID[id] = sub
	}
	c.mu.Unlock()
	return &Subscription{Events: ch, conn: c, methods: methods, id: id}
}

// err returns the connection's terminal error, or nil if still live.
func (c *Conn) err() error {
	v := c.connErr.Load()
	if v == nil {
		return nil
	}
	if err, ok := v.(error); ok && err != nil {
		return err
	}
	return nil
}

// Err returns the read-loop's terminal error, or nil while the
// connection is still live. Useful for test assertions.
func (c *Conn) Err() error {
	return c.err()
}

// Close closes the underlying connection. The read loop notices the
// resulting read error and fails any in-flight Send calls.
func (c *Conn) Close() error {
	if err := c.conn.Close(); err != nil {
		// Swallow the common "already closed" case so callers can
		// freely defer Close without layering.
		if !errors.Is(err, net.ErrClosed) {
			return err
		}
	}
	return nil
}
