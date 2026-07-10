package mikrotik

import (
	"sync"
	"time"
)

type QuerySafeResult struct {
	IsError bool
	Data    []QueryResult
	Error   *RouterOSAPIError
}

type Client struct {
	pool   *Pool
	mu     sync.Mutex
	closed bool
}

func NewClient(config Config) (*Client, error) {
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

	pool := NewPool(config)

	client := &Client{
		pool: pool,
	}

	return client, nil
}

func (c *Client) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return &ConnectionError{RouterOSAPIError{Message: "client is closed"}}
	}

	return c.pool.Init()
}

func (c *Client) Query(cmd []string) ([]QueryResult, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, &ConnectionError{RouterOSAPIError{Message: "client is closed"}}
	}
	c.mu.Unlock()

	result, err := c.pool.Execute(cmd)
	if err != nil {
		return nil, err
	}

	if result == nil {
		return nil, nil
	}

	if r, ok := result.([]QueryResult); ok {
		return r, nil
	}

	if r, ok := result.([]interface{}); ok {
		qr := make([]QueryResult, len(r))
		for i, v := range r {
			if m, ok := v.(map[string]string); ok {
				qr[i] = QueryResult(m)
			} else if m, ok := v.(QueryResult); ok {
				qr[i] = m
			}
		}
		return qr, nil
	}

	return nil, nil
}

func (c *Client) QuerySafe(cmd []string) QuerySafeResult {
	data, err := c.Query(cmd)
	if err != nil {
		apiErr, ok := err.(*RouterOSAPIError)
		if !ok {
			apiErr = &RouterOSAPIError{Message: err.Error()}
		}
		return QuerySafeResult{IsError: true, Error: apiErr}
	}
	return QuerySafeResult{Data: data}
}

func (c *Client) On(event string, cb func(interface{})) {
	c.pool.On(event, cb)
}

func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.closed = true
	c.pool.Close()
}
