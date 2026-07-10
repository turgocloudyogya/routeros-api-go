package mikrotik

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"sync"
	"time"
)

type SSLOptions struct {
	Cert       string
	Key        string
	CA         string
	SkipVerify bool
}

type RetryConfig struct {
	Retries  int
	MinDelay time.Duration
	MaxDelay time.Duration
}

type HealthCheckConfig struct {
	Interval time.Duration
	Timeout  time.Duration
	Command  []string
}

type Config struct {
	Host         string
	Port         int
	SSL          bool
	SSLOptions   *SSLOptions
	Username     string
	Password     string
	Timeout      time.Duration
	QueryTimeout time.Duration
	PoolSize     int
	AutoConnect  bool
	IdleTimeout  time.Duration
	Retry        *RetryConfig
	HealthCheck  *HealthCheckConfig
}

type PoolStats struct {
	TotalConnections      int
	ActiveConnections     int
	DestroyedConnections  int
	QueuedTasks           int
	TotalQueries          int64
	FailedQueries         int64
	Uptime                time.Duration
}

type Pool struct {
	connections []*Connection
	mu          sync.Mutex
	index       int
	config      Config
	listeners   map[string][]func(interface{})
	inited      bool
	startTime   time.Time
	healthStop  chan struct{}

	totalQueries  int64
	failedQueries int64
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
	if config.Retry == nil {
		config.Retry = &RetryConfig{Retries: 0, MinDelay: time.Second, MaxDelay: 30 * time.Second}
	}

	return &Pool{
		config:      config,
		listeners:   make(map[string][]func(interface{})),
		connections: make([]*Connection, 0, config.PoolSize),
		startTime:   time.Now(),
	}
}

func (p *Pool) Init() error {
	p.mu.Lock()
	if p.inited {
		p.mu.Unlock()
		return nil
	}
	cfg := p.config
	p.mu.Unlock()

	var lastErr error
	for i := 0; i < cfg.PoolSize; i++ {
		conn := p.newConnection(cfg)
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

	p.mu.Lock()
	p.inited = true
	p.mu.Unlock()

	p.startHealthChecks()
	return nil
}

func (p *Pool) startHealthChecks() {
	if p.config.HealthCheck != nil && p.config.HealthCheck.Interval > 0 {
		p.healthStop = make(chan struct{})
		go func() {
			ticker := time.NewTicker(p.config.HealthCheck.Interval)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					cmd := p.config.HealthCheck.Command
					if len(cmd) == 0 {
						cmd = []string{"/system/identity/print"}
					}
					ctx := context.Background()
					if p.config.HealthCheck.Timeout > 0 {
						var cancel context.CancelFunc
						ctx, cancel = context.WithTimeout(ctx, p.config.HealthCheck.Timeout)
						defer cancel()
					}
					_, _ = p.ExecuteContext(ctx, cmd)
				case <-p.healthStop:
					return
				}
			}
		}()
	}
}

func (p *Pool) stopHealthChecks() {
	if p.healthStop != nil {
		close(p.healthStop)
		p.healthStop = nil
	}
}

func (p *Pool) newConnection(cfg Config) *Connection {
	tlsCfg := p.resolveTLSConfig()
	return NewConnection(
		cfg.Host, cfg.Port,
		tlsCfg,
		cfg.Username, cfg.Password,
		cfg.Timeout, cfg.QueryTimeout, cfg.IdleTimeout,
	)
}

func (p *Pool) resolveTLSConfig() *tls.Config {
	if !p.config.SSL && p.config.SSLOptions == nil {
		return nil
	}
	tc := &tls.Config{}
	if p.config.SSLOptions != nil {
		if p.config.SSLOptions.Cert != "" || p.config.SSLOptions.Key != "" {
			cert, err := tls.X509KeyPair([]byte(p.config.SSLOptions.Cert), []byte(p.config.SSLOptions.Key))
			if err == nil {
				tc.Certificates = []tls.Certificate{cert}
			}
		}
		if p.config.SSLOptions.CA != "" {
			caPool := x509.NewCertPool()
			caPool.AppendCertsFromPEM([]byte(p.config.SSLOptions.CA))
			tc.RootCAs = caPool
		}
		tc.InsecureSkipVerify = p.config.SSLOptions.SkipVerify
	} else {
		tc.InsecureSkipVerify = true
	}
	return tc
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

func (p *Pool) ensureReady() error {
	var toReconnect []*Connection

	p.mu.Lock()
	for _, conn := range p.connections {
		if !conn.IsConnected() && !conn.IsDestroyed() {
			toReconnect = append(toReconnect, conn)
		}
	}
	allDestroyed := len(p.connections) > 0
	for _, conn := range p.connections {
		if !conn.IsDestroyed() {
			allDestroyed = false
			break
		}
	}
	p.mu.Unlock()

	if allDestroyed {
		p.mu.Lock()
		p.connections = nil
		p.inited = false
		p.mu.Unlock()
		return p.Init()
	}

	for _, conn := range toReconnect {
		if err := conn.Connect(); err != nil {
			return err
		}
	}

	return nil
}

func (p *Pool) Execute(cmd []string) (interface{}, error) {
	return p.ExecuteContext(context.Background(), cmd)
}

func (p *Pool) ExecuteContext(ctx context.Context, cmd []string) (interface{}, error) {
	if err := p.ensureReady(); err != nil {
		return nil, err
	}

	retryCfg := p.config.Retry
	var lastErr error

	for attempt := 0; attempt <= retryCfg.Retries; attempt++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		conn := p.acquire()
		if conn == nil {
			return nil, &ConnectionError{RouterOSAPIError{Message: "no available connections"}}
		}

		result, err := conn.ExecuteContext(ctx, cmd)
		if err == nil {
			p.incTotalQueries()
			return result, nil
		}

		lastErr = err

		if apiErr, ok := err.(*RouterOSAPIError); ok {
			if _, isAuth := err.(*AuthenticationError); isAuth {
				p.incFailedQueries()
				return nil, err
			}
			_ = apiErr
		}

		p.incFailedQueries()

		if attempt < retryCfg.Retries {
			delay := retryCfg.MinDelay * (1 << attempt)
			if delay > retryCfg.MaxDelay {
				delay = retryCfg.MaxDelay
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}
	}

	return nil, &RetryError{RouterOSAPIError{
		Message: "max retries exceeded",
		Cause:   lastErr,
	}}
}

func (p *Pool) ExecuteStream(ctx context.Context, cmd []string, onRow func(QueryResult)) ([]QueryResult, error) {
	if err := p.ensureReady(); err != nil {
		return nil, err
	}
	conn := p.acquire()
	if conn == nil {
		return nil, &ConnectionError{RouterOSAPIError{Message: "no available connections"}}
	}
	return conn.ExecuteStream(ctx, cmd, onRow)
}

func (p *Pool) incTotalQueries() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.totalQueries++
}

func (p *Pool) incFailedQueries() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failedQueries++
}

func (p *Pool) GetStats() PoolStats {
	p.mu.Lock()
	defer p.mu.Unlock()

	active := 0
	destroyed := 0
	for _, c := range p.connections {
		if c.IsConnected() {
			active++
		}
		if c.IsDestroyed() {
			destroyed++
		}
	}

	return PoolStats{
		TotalConnections:     len(p.connections),
		ActiveConnections:    active,
		DestroyedConnections: destroyed,
		TotalQueries:         p.totalQueries,
		FailedQueries:        p.failedQueries,
		Uptime:               time.Since(p.startTime),
	}
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
	p.stopHealthChecks()
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, conn := range p.connections {
		conn.Close()
	}
	p.connections = nil
	p.inited = false
}
