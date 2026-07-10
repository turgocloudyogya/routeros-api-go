package mikrotik

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"sync"
	"time"
)

type taskResult struct {
	data interface{}
	err  error
}

type task struct {
	id     string
	cmd    []string
	result chan *taskResult
}

type streamState struct {
	rows  []QueryResult
	onRow func(QueryResult)
	done  chan struct{}
}

type Connection struct {
	conn          net.Conn
	mu            sync.Mutex
	queue         []*task
	pending       *task
	buffer        []byte
	host          string
	port          int
	tlsConfig     *tls.Config
	username      string
	password      string
	timeout       time.Duration
	queryTimeout  time.Duration
	idleTimeout   time.Duration
	lastActivity  time.Time
	connected     bool
	authenticated bool
	destroyed     bool
	closed        bool
	listeners     map[string][]func(interface{})
	idleTimer     *time.Timer

	streamState *streamState

	totalQueries  int64
	failedQueries int64
}

func NewConnection(host string, port int, tlsConfig *tls.Config, username, password string, timeout, queryTimeout, idleTimeout time.Duration) *Connection {
	c := &Connection{
		host:         host,
		port:         port,
		tlsConfig:    tlsConfig,
		username:     username,
		password:     password,
		timeout:      timeout,
		queryTimeout: queryTimeout,
		idleTimeout:  idleTimeout,
		lastActivity: time.Now(),
		listeners:    make(map[string][]func(interface{})),
	}
	if idleTimeout > 0 {
		c.idleTimer = time.AfterFunc(idleTimeout, c.checkIdle)
	}
	return c
}

func (c *Connection) checkIdle() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.destroyed || !c.connected {
		return
	}
	if time.Since(c.lastActivity) >= c.idleTimeout {
		c.closeSocket()
		c.idleTimer = time.AfterFunc(c.idleTimeout, c.checkIdle)
	}
}

func (c *Connection) Connect() error {
	c.mu.Lock()
	if c.destroyed {
		c.mu.Unlock()
		return &ConnectionError{RouterOSAPIError{Message: "connection is destroyed"}}
	}
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
	c.connected = false
	c.authenticated = false
	c.closed = false
	c.buffer = nil
	c.mu.Unlock()

	addr := net.JoinHostPort(c.host, fmt.Sprintf("%d", c.port))
	var dialer net.Conn
	var err error

	if c.tlsConfig != nil {
		dialer, err = tls.DialWithDialer(&net.Dialer{Timeout: c.timeout}, "tcp", addr, c.tlsConfig)
	} else {
		dialer, err = net.DialTimeout("tcp", addr, c.timeout)
	}
	if err != nil {
		return &ConnectionError{RouterOSAPIError{Message: fmt.Sprintf("connection failed: %s", err.Error())}}
	}

	c.mu.Lock()
	if c.destroyed {
		c.mu.Unlock()
		dialer.Close()
		return &ConnectionError{RouterOSAPIError{Message: "connection is destroyed"}}
	}
	c.conn = dialer
	c.connected = true
	c.lastActivity = time.Now()
	c.mu.Unlock()

	c.emit("connected", nil)
	go c.readLoop()

	if err := c.login(); err != nil {
		c.Close()
		return err
	}

	return nil
}

func (c *Connection) login() error {
	result := make(chan *taskResult, 1)
	t := &task{
		id:     generateID("/login"),
		cmd:    []string{"/login", fmt.Sprintf("=name=%s", c.username), fmt.Sprintf("=password=%s", c.password)},
		result: result,
	}

	c.mu.Lock()
	c.queue = append(c.queue, t)
	c.mu.Unlock()
	c.sendNext()

	r := <-result
	if r.err != nil {
		return r.err
	}

	c.mu.Lock()
	c.authenticated = true
	c.mu.Unlock()
	return nil
}

func (c *Connection) readLoop() {
	buf := make([]byte, 4096)
	for {
		c.mu.Lock()
		conn := c.conn
		c.mu.Unlock()

		if conn == nil {
			return
		}

		n, err := conn.Read(buf)
		if err != nil {
			c.emit("error", map[string]string{"error": err.Error()})
			c.closeSocket()
			return
		}

		chunk := make([]byte, n)
		copy(chunk, buf[:n])

		c.mu.Lock()
		c.lastActivity = time.Now()
		c.resetIdleTimer()
		c.buffer = append(c.buffer, chunk...)

		words := decodeWord(c.buffer)
		if len(words) == 0 {
			c.mu.Unlock()
			continue
		}

		// Emit streaming !re rows before terminator
		c.emitStreamRows(words)

		hasDone, hasTrap := false, false
		for _, w := range words {
			if w == "!done" {
				hasDone = true
			} else if w == "!trap" {
				hasTrap = true
			}
		}

		if !hasDone && !hasTrap {
			c.mu.Unlock()
			continue
		}

		c.buffer = nil
		t := c.pending
		c.pending = nil
		c.mu.Unlock()

		parsed := parseResponse(words)

		if t != nil {
			c.emit("receive", map[string]interface{}{
				"id":   t.id,
				"data": parsed,
			})

			if len(parsed) > 0 {
				if msg, ok := parsed[0]["message"]; ok && msg != "" {
					var err error
					if t.cmd[0] == "/login" {
						err = &AuthenticationError{RouterOSAPIError{Message: msg, ID: t.id, Detail: parsed[0]}}
					} else {
						err = &ProtocolError{RouterOSAPIError{Message: msg, ID: t.id, Detail: parsed[0]}}
					}
					if c.streamState != nil {
						c.mu.Lock()
						c.streamState.done <- struct{}{}
						c.mu.Unlock()
					}
					t.result <- &taskResult{err: err}
					close(t.result)
					c.sendNext()
					continue
				}
			}

			if c.streamState != nil {
				c.mu.Lock()
				c.streamState.done <- struct{}{}
				c.mu.Unlock()
			}
			t.result <- &taskResult{data: parsed}
			close(t.result)
		}

		c.sendNext()
	}
}

func (c *Connection) emitStreamRows(words []string) {
	if c.streamState == nil {
		return
	}

	sentences := splitSentences(words)
	endsWithTerm := len(words) > 0 && words[len(words)-1] == ""

	limit := len(sentences)
	if !endsWithTerm && limit > 0 {
		limit = len(sentences) - 1
	}

	for i := 0; i < limit; i++ {
		s := sentences[i]
		if len(s) == 0 {
			continue
		}
		if s[0] == "!re" {
			parsed := parseResponse(s)
			for _, row := range parsed {
				c.streamState.rows = append(c.streamState.rows, row)
				if c.streamState.onRow != nil {
					c.streamState.onRow(row)
				}
				c.emit("row", map[string]interface{}{"data": row})
			}
		}
	}
}

func splitSentences(words []string) [][]string {
	var sentences [][]string
	var current []string
	for _, w := range words {
		if w == "" {
			if len(current) > 0 {
				sentences = append(sentences, current)
				current = nil
			}
		} else {
			current = append(current, w)
		}
	}
	if len(current) > 0 {
		sentences = append(sentences, current)
	}
	return sentences
}

func (c *Connection) sendNext() {
	c.mu.Lock()
	if c.pending != nil || len(c.queue) == 0 {
		c.mu.Unlock()
		return
	}
	t := c.queue[0]
	c.queue = c.queue[1:]
	c.pending = t
	data := buildCommand(t.cmd)
	c.lastActivity = time.Now()
	c.resetIdleTimer()
	c.mu.Unlock()

	c.emit("send", map[string]interface{}{
		"id":  t.id,
		"cmd": t.cmd,
	})

	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()

	if conn == nil {
		t.result <- &taskResult{err: &ConnectionError{RouterOSAPIError{Message: "connection is nil", ID: t.id}}}
		close(t.result)
		return
	}

	if _, err := conn.Write(data); err != nil {
		c.mu.Lock()
		if c.pending == t {
			c.pending = nil
		}
		c.mu.Unlock()
		t.result <- &taskResult{err: &ConnectionError{RouterOSAPIError{Message: fmt.Sprintf("write failed: %s", err.Error()), ID: t.id}}}
		close(t.result)
	}
}

func (c *Connection) Execute(cmd []string) (interface{}, error) {
	return c.ExecuteContext(context.Background(), cmd)
}

func (c *Connection) ExecuteContext(ctx context.Context, cmd []string) (interface{}, error) {
	c.mu.Lock()
	if !c.destroyed && (!c.connected || c.closed) {
		c.mu.Unlock()
		if err := c.Connect(); err != nil {
			return nil, err
		}
		c.mu.Lock()
	}
	if c.destroyed {
		c.mu.Unlock()
		return nil, &ConnectionError{RouterOSAPIError{Message: "connection is destroyed"}}
	}
	c.mu.Unlock()

	id := generateID(cmd[0])
	result := make(chan *taskResult, 1)
	t := &task{
		id:     id,
		cmd:    cmd,
		result: result,
	}

	c.mu.Lock()
	if c.destroyed {
		c.mu.Unlock()
		return nil, &ConnectionError{RouterOSAPIError{Message: "connection is destroyed"}}
	}
	c.queue = append(c.queue, t)
	c.mu.Unlock()

	c.sendNext()

	select {
	case r := <-result:
		if r.err != nil {
			return nil, r.err
		}
		return r.data, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *Connection) ExecuteStream(ctx context.Context, cmd []string, onRow func(QueryResult)) ([]QueryResult, error) {
	c.mu.Lock()
	if !c.destroyed && (!c.connected || c.closed) {
		c.mu.Unlock()
		if err := c.Connect(); err != nil {
			return nil, err
		}
		c.mu.Lock()
	}
	if c.destroyed {
		c.mu.Unlock()
		return nil, &ConnectionError{RouterOSAPIError{Message: "connection is destroyed"}}
	}
	c.mu.Unlock()

	ss := &streamState{
		rows:  nil,
		onRow: onRow,
		done:  make(chan struct{}, 1),
	}

	c.mu.Lock()
	c.streamState = ss
	streamPending := c.pending
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		c.streamState = nil
		c.mu.Unlock()
	}()

	// If there's already a pending task in stream mode, wait for its completion
	if streamPending != nil {
		select {
		case <-ss.done:
			return ss.rows, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	id := generateID(cmd[0])
	result := make(chan *taskResult, 1)
	t := &task{
		id:     id,
		cmd:    cmd,
		result: result,
	}

	c.mu.Lock()
	if c.destroyed {
		c.mu.Unlock()
		return nil, &ConnectionError{RouterOSAPIError{Message: "connection is destroyed"}}
	}
	c.queue = append(c.queue, t)
	c.mu.Unlock()

	c.sendNext()

	select {
	case <-result:
		return ss.rows, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-ss.done:
		return ss.rows, nil
	}
}

func (c *Connection) closeSocket() {
	c.closed = true
	c.connected = false
	c.authenticated = false
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
	for _, t := range c.queue {
		t.result <- &taskResult{err: &ConnectionError{RouterOSAPIError{Message: "connection closed", ID: t.id}}}
		close(t.result)
	}
	c.queue = nil
	if c.pending != nil {
		c.pending.result <- &taskResult{err: &ConnectionError{RouterOSAPIError{Message: "connection closed", ID: c.pending.id}}}
		close(c.pending.result)
		c.pending = nil
	}
}

func (c *Connection) resetIdleTimer() {
	if c.idleTimer != nil {
		c.idleTimer.Reset(c.idleTimeout)
	}
}

func (c *Connection) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.destroyed = true
	if c.idleTimer != nil {
		c.idleTimer.Stop()
		c.idleTimer = nil
	}
	c.closeSocket()
}

func (c *Connection) emit(event string, data interface{}) {
	c.mu.Lock()
	listeners := make([]func(interface{}), len(c.listeners[event]))
	copy(listeners, c.listeners[event])
	c.mu.Unlock()

	for _, cb := range listeners {
		cb(data)
	}
}

func (c *Connection) On(event string, cb func(interface{})) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.listeners[event] = append(c.listeners[event], cb)
}

func (c *Connection) Off(event string, cb func(interface{})) {
	c.mu.Lock()
	defer c.mu.Unlock()
	listeners := c.listeners[event]
	for i, fn := range listeners {
		ptr := fmt.Sprintf("%p", fn)
		cbPtr := fmt.Sprintf("%p", cb)
		if ptr == cbPtr {
			c.listeners[event] = append(listeners[:i], listeners[i+1:]...)
			return
		}
	}
}

func (c *Connection) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected && c.authenticated
}

func (c *Connection) IsDestroyed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.destroyed
}

var idCounter int64

func generateID(firstCmd string) string {
	idCounter++
	return fmt.Sprintf("%s-%x", firstCmd, idCounter)
}
