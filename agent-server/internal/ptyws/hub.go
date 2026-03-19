package ptyws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"nhooyr.io/websocket"
)

const (
	closeReasonClientSlow = "client too slow"
	closeReasonConnClosed = "connection closed"
	exitWriteTimeout      = 2 * time.Second
	streamWriteTimeout    = 10 * time.Second
)

type controlMessage struct {
	Type string `json:"type"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
}

type outboundMessage struct {
	messageType websocket.MessageType
	payload     []byte
}

type client struct {
	id        int64
	conn      *websocket.Conn
	send      chan outboundMessage
	closeOnce sync.Once
}

// Hub multiplexes PTY I/O across many websocket clients.
type Hub struct {
	input io.Writer

	resizeFn func(cols, rows int) error

	replay *ReplayBuffer

	mu      sync.Mutex
	clients map[int64]*client
	closed  bool
	nextID  atomic.Int64

	inputMu sync.Mutex
}

func NewHub(input io.Writer, replayBytes int, resizeFn func(cols, rows int) error) *Hub {
	return &Hub{
		input:    input,
		resizeFn: resizeFn,
		replay:   NewReplayBuffer(replayBytes),
		clients:  make(map[int64]*client),
	}
}

// BroadcastOutput broadcasts PTY output bytes to all attached clients.
func (h *Hub) BroadcastOutput(payload []byte) {
	if len(payload) == 0 {
		return
	}

	h.replay.Append(payload)

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	clients := make([]*client, 0, len(h.clients))
	for _, c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.Unlock()

	for _, c := range clients {
		msg := outboundMessage{messageType: websocket.MessageBinary, payload: cloneBytes(payload)}
		if !trySend(c.send, msg) {
			_ = h.detach(c.id, websocket.StatusPolicyViolation, closeReasonClientSlow)
		}
	}
}

// NotifyExit sends final exit control message and closes all clients.
func (h *Hub) NotifyExit(exitCode int) {
	control := map[string]any{
		"type":      "exit",
		"exit_code": exitCode,
	}
	encoded, err := json.Marshal(control)
	if err != nil {
		encoded = []byte(`{"type":"exit","exit_code":0}`)
	}

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	clients := make([]*client, 0, len(h.clients))
	for _, c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.Unlock()

	for _, c := range clients {
		h.closeClient(c, websocket.StatusNormalClosure, "run exited", encoded)
	}

	h.mu.Lock()
	h.clients = make(map[int64]*client)
	h.mu.Unlock()
}

// ServeConn manages one websocket lifecycle.
func (h *Hub) ServeConn(ctx context.Context, conn *websocket.Conn) error {
	if conn == nil {
		return fmt.Errorf("nil websocket connection")
	}

	cl := &client{
		id:   h.nextID.Add(1),
		conn: conn,
		send: make(chan outboundMessage, 128),
	}

	if err := h.attach(cl); err != nil {
		_ = conn.Close(websocket.StatusPolicyViolation, err.Error())
		return err
	}
	defer func() { _ = h.detach(cl.id, websocket.StatusNormalClosure, closeReasonConnClosed) }()

	replay := h.replay.Bytes()
	if len(replay) > 0 {
		writeCtx, cancel := context.WithTimeout(ctx, exitWriteTimeout)
		err := conn.Write(writeCtx, websocket.MessageBinary, replay)
		cancel()
		if err != nil {
			return err
		}
	}

	errCh := make(chan error, 2)
	go func() { errCh <- h.writerLoop(ctx, cl) }()
	go func() { errCh <- h.readerLoop(ctx, cl) }()

	select {
	case <-ctx.Done():
		_ = conn.Close(websocket.StatusNormalClosure, "context done")
		return nil
	case err := <-errCh:
		if err == nil {
			return nil
		}
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}
}

func (h *Hub) attach(c *client) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return fmt.Errorf("pty stream is closed")
	}
	h.clients[c.id] = c
	return nil
}

func (h *Hub) detach(id int64, status websocket.StatusCode, reason string) error {
	h.mu.Lock()
	cl, ok := h.clients[id]
	if ok {
		delete(h.clients, id)
	}
	h.mu.Unlock()
	if !ok {
		return nil
	}

	h.closeClient(cl, status, reason, nil)
	return nil
}

func (h *Hub) writerLoop(ctx context.Context, c *client) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case msg, ok := <-c.send:
			if !ok {
				return nil
			}
			writeCtx, cancel := context.WithTimeout(ctx, streamWriteTimeout)
			err := c.conn.Write(writeCtx, msg.messageType, msg.payload)
			cancel()
			if err != nil {
				return err
			}
		}
	}
}

func (h *Hub) readerLoop(ctx context.Context, c *client) error {
	for {
		typ, payload, err := c.conn.Read(ctx)
		if err != nil {
			var closeErr websocket.CloseError
			if errors.As(err, &closeErr) {
				return nil
			}
			return err
		}

		switch typ {
		case websocket.MessageBinary:
			if err := h.writeInput(payload); err != nil {
				return err
			}
		case websocket.MessageText:
			if err := h.handleControl(payload); err != nil {
				// Malformed control frames are ignored to keep PTY sessions resilient.
				continue
			}
		default:
			continue
		}
	}
}

func (h *Hub) writeInput(payload []byte) error {
	if len(payload) == 0 {
		return nil
	}
	h.inputMu.Lock()
	defer h.inputMu.Unlock()
	_, err := h.input.Write(payload)
	if err != nil {
		return fmt.Errorf("write PTY input: %w", err)
	}
	return nil
}

// WriteInput injects bytes into the underlying PTY input stream.
func (h *Hub) WriteInput(payload []byte) error {
	return h.writeInput(payload)
}

func (h *Hub) handleControl(payload []byte) error {
	var msg controlMessage
	if err := json.Unmarshal(payload, &msg); err != nil {
		return fmt.Errorf("decode control message: %w", err)
	}
	if msg.Type != "resize" {
		return fmt.Errorf("unsupported control message type: %s", msg.Type)
	}
	if msg.Cols <= 0 || msg.Rows <= 0 {
		return fmt.Errorf("invalid resize dimensions")
	}
	if h.resizeFn == nil {
		return fmt.Errorf("resize is not supported")
	}
	if err := h.resizeFn(msg.Cols, msg.Rows); err != nil {
		return fmt.Errorf("resize PTY: %w", err)
	}
	return nil
}

func cloneBytes(in []byte) []byte {
	out := make([]byte, len(in))
	copy(out, in)
	return out
}

func safeClose(ch chan outboundMessage) {
	defer func() {
		_ = recover()
	}()
	close(ch)
}

func trySend(ch chan outboundMessage, msg outboundMessage) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	select {
	case ch <- msg:
		return true
	default:
		return false
	}
}

func (h *Hub) closeClient(c *client, status websocket.StatusCode, reason string, exitPayload []byte) {
	if c == nil {
		return
	}
	c.closeOnce.Do(func() {
		if len(exitPayload) > 0 {
			ctx, cancel := context.WithTimeout(context.Background(), exitWriteTimeout)
			_ = c.conn.Write(ctx, websocket.MessageText, exitPayload)
			cancel()
		}
		safeClose(c.send)
		_ = c.conn.Close(status, reason)
	})
}
