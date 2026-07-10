package mikrotik

import (
	"sync"
	"time"
)

type Config struct {
	Host         string
	Port         int
	SSL          bool
	Username     string
	Password     string
	Timeout      time.Duration
	QueryTimeout time.Duration
	PoolSize     int
}

type Pool struct {
	connections []*Connection
	mu          sync.Mutex
	index       int
	config      Config
	listeners   map[string][]func(interface{})
}

func NewPool(config Config) *Pool {
	if config.Host == "" {
		config.Host = "192.168.88.1"
	}
	if config.Port == 0 {
		config.Port = 8728
	}
	if config.Username == "" {
		config.Username = "admin"
	}
	if config.Timeout == 0 {
		config.Timeout = 5 * time.Second
	}
	if config.PoolSize < 1 {
		config.PoolSize = 3
	}

	return &Pool{
		config:      config,
		listeners:   make(map[string][]func(interface{})),
		connections: make([]*Connection, 0, config.PoolSize),
	}
}

func (p *Pool) Init() error {
	p.mu.Lock()
	cfg := p.config
	p.mu.Unlock()

	var lastErr error
	for i := 0; i < cfg.PoolSize; i++ {
		conn := NewConnection(cfg.Host, cfg.Port, cfg.SSL, cfg.Username, cfg.Password, cfg.Timeout, cfg.QueryTimeout)
		p.propagateEvents(conn)

		if err := conn.Connect(); err != nil {
			lastErr = err
			continue
		}

		p.mu.Lock()
		p.connections = append(p.connections, conn)
		p.mu.Unlock()
	}

	if len(p.connections) == 0 && lastErr != nil {
		return lastErr
	}

	return nil
}

func (p *Pool) propagateEvents(conn *Connection) {
	for event, listeners := range p.listeners {
		for _, cb := range listeners {
			conn.On(event, cb)
		}
	}
}

func (p *Pool) acquire() *Connection {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.connections) == 0 {
		return nil
	}

	conn := p.connections[p.index%len(p.connections)]
	p.index++
	return conn
}

func (p *Pool) Execute(cmd []string) (interface{}, error) {
	conn := p.acquire()
	if conn == nil {
		return nil, &ConnectionError{RouterOSAPIError{Message: "no available connections"}}
	}
	return conn.Execute(cmd)
}

func (p *Pool) On(event string, cb func(interface{})) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.listeners[event] = append(p.listeners[event], cb)
	for _, conn := range p.connections {
		conn.On(event, cb)
	}
}

func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, conn := range p.connections {
		conn.Close()
	}
	p.connections = nil
}
