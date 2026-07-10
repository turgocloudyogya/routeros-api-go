package mikrotik

import (
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

type Connection struct {
	conn          net.Conn
	mu            sync.Mutex
	queue         []*task
	pending       *task
	buffer        []byte
	host          string
	port          int
	ssl           bool
	username      string
	password      string
	timeout       time.Duration
	queryTimeout  time.Duration
	connected     bool
	authenticated bool
	destroyed     bool
	listeners     map[string][]func(interface{})
}

func NewConnection(host string, port int, ssl bool, username, password string, timeout, queryTimeout time.Duration) *Connection {
	return &Connection{
		host:         host,
		port:         port,
		ssl:          ssl,
		username:     username,
		password:     password,
		timeout:      timeout,
		queryTimeout: queryTimeout,
		listeners:    make(map[string][]func(interface{})),
	}
}

func (c *Connection) Connect() error {
	var dialer net.Conn
	var err error

	if c.ssl {
		dialer, err = tls.DialWithDialer(&net.Dialer{Timeout: c.timeout}, "tcp",
			fmt.Sprintf("%s:%d", c.host, c.port),
			&tls.Config{InsecureSkipVerify: true})
	} else {
		dialer, err = net.DialTimeout("tcp", fmt.Sprintf("%s:%d", c.host, c.port), c.timeout)
	}
	if err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}

	c.mu.Lock()
	if c.destroyed {
		c.mu.Unlock()
		dialer.Close()
		return &ConnectionError{RouterOSAPIError{Message: "connection is destroyed"}}
	}
	c.conn = dialer
	c.connected = true
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
	id := generateID("/login")
	t := &task{
		id:     id,
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
		n, err := c.conn.Read(buf)
		if err != nil {
			c.emit("error", map[string]string{"error": err.Error()})
			c.Close()
			return
		}

		chunk := make([]byte, n)
		copy(chunk, buf[:n])

		c.mu.Lock()
		c.buffer = append(c.buffer, chunk...)
		words := decodeWord(c.buffer)

		hasDone := false
		for _, w := range words {
			if w == "!done" {
				hasDone = true
				break
			}
		}

		if !hasDone && !hasTrap(words) {
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
					if len(t.cmd) > 0 && t.cmd[0] == "/login" {
						t.result <- &taskResult{err: &AuthenticationError{RouterOSAPIError{Message: msg, ID: t.id, Detail: parsed[0]}}}
					} else {
						t.result <- &taskResult{err: &ProtocolError{RouterOSAPIError{Message: msg, ID: t.id, Detail: parsed[0]}}}
					}
					close(t.result)
					c.sendNext()
					continue
				}
			}
			t.result <- &taskResult{data: parsed}
			close(t.result)
		}

		c.sendNext()
	}
}

func hasTrap(words []string) bool {
	for _, w := range words {
		if w == "!trap" {
			return true
		}
	}
	return false
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
	c.mu.Unlock()

	c.emit("send", map[string]interface{}{
		"id":  t.id,
		"cmd": t.cmd,
	})

	if _, err := c.conn.Write(data); err != nil {
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

	if c.queryTimeout > 0 {
		select {
		case r := <-result:
			return r.data, r.err
		case <-time.After(c.queryTimeout):
			return nil, &TimeoutError{RouterOSAPIError{Message: "query timeout"}}
		}
	}

	r := <-result
	return r.data, r.err
}

func (c *Connection) Close() {
	c.mu.Lock()
	c.destroyed = true
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
	c.mu.Unlock()
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

var idCounter int64

func generateID(firstCmd string) string {
	idCounter++
	return fmt.Sprintf("%s-%x", firstCmd, idCounter)
}
