# RouterOS API — Go

Go port of the [Node.js routeros-api](https://github.com/turgocloudyogya/routeros-api).  
A Mikrotik RouterOS API client with connection pooling, request queuing, event-based communication, typed errors, context support, streaming, retry with backoff, and health checks.

## Features

- **Connection Pool** — multiple TCP connections for parallel request handling
- **Request Queue** — sequential command execution per connection (RouterOS protocol requires one command at a time)
- **SSL/TLS** — connect via port 8729 with TLS; supports custom cert, key, CA, and `SkipVerify`
- **Auto-Connect** — connects automatically on first query when `AutoConnect: true`
- **Idle Timeout** — closes socket after inactivity, reopens on next query
- **Events** — listen to `connected`, `disconnected`, `send`, `receive`, `error`, `row` events
- **Query Timeout** — separate timeout for per-query response
- **Context Support** — `QueryContext`, `QuerySafeContext`, `ExecuteContext` with `context.Context` for cancellation/timeout
- **Streaming** — `QueryStream`, `ExecuteStream` with per-row callback
- **QuerySafe** — never-throw variant that returns `QuerySafeResult`
- **Retry** — configurable exponential backoff on failure (`Retries`, `MinDelay`, `MaxDelay`)
- **Health Check** — periodic keep-alive queries via `HealthCheck` config
- **Stats** — `GetStats()` returns `PoolStats` with connection/query counters and uptime
- **Typed Errors** — `RouterOSAPIError` with sub-types: `TimeoutError`, `AuthenticationError`, `ConnectionError`, `ProtocolError`, `RetryError`
- **No external dependencies** — uses only the Go standard library

## Installation

```bash
go get github.com/turgocloudyogya/routeros-api-go
```

## Quick Start

```go
package main

import (
    "fmt"
    "log"
    routeros "github.com/turgocloudyogya/routeros-api-go"
)

func main() {
    client, err := routeros.NewClient(routeros.Config{
        Host:     "192.168.88.1",
        Port:     8728,
        Username: "admin",
        Password: "your-password",
        PoolSize: 3,
    })
    if err != nil {
        log.Fatal(err)
    }

    if err := client.Connect(); err != nil {
        log.Fatal(err)
    }

    result, err := client.Query([]string{"/ip/address/print"})
    if err != nil {
        log.Fatal(err)
    }

    for _, r := range result {
        fmt.Printf("Address: %s\n", r["address"])
    }

    client.Close()
}
```

## Configuration

| Field          | Type                | Default          | Description                              |
|----------------|---------------------|------------------|------------------------------------------|
| `Host`         | `string`            | `192.168.88.1`   | Router IP/hostname                       |
| `Port`         | `int`               | `8728`           | API port (`8729` for SSL)                |
| `SSL`          | `bool \| SSLOptions`| `false`          | Enable TLS / TLS options                 |
| `Username`     | `string`            | `admin`          | Login username                           |
| `Password`     | `string`            | `""`             | Login password                           |
| `Timeout`      | `time.Duration`     | `5s`             | Connection timeout                       |
| `QueryTimeout` | `time.Duration`     | `0`              | Per-query timeout (0 = no timeout)       |
| `PoolSize`     | `int`               | `3`              | Number of connections in the pool        |
| `AutoConnect`  | `bool`              | `true`           | Auto-connect on first query              |
| `IdleTimeout`  | `time.Duration`     | `0`              | Close socket after idle (0 = disabled)   |
| `Retry`        | `*RetryConfig`      | `nil`            | Retry with exponential backoff           |
| `HealthCheck`  | `*HealthCheckConfig`| `nil`            | Periodic health check config             |

### SSLOptions

```go
type SSLOptions struct {
    Cert       string // Custom client certificate
    Key        string // Client certificate key
    CA         string // Custom CA certificate
    SkipVerify bool   // Skip TLS verification (default: true)
}
```

### RetryConfig

```go
type RetryConfig struct {
    Retries  int           // Max retry attempts
    MinDelay time.Duration // Initial backoff delay
    MaxDelay time.Duration // Maximum backoff delay
}
```

### HealthCheckConfig

```go
type HealthCheckConfig struct {
    Interval time.Duration // Interval between health checks
    Timeout  time.Duration // Query timeout for health check (optional)
    Command  []string      // Custom command (default: ["/system/identity/print"])
}
```

## API

### `client.Query(command)`

Send a command to RouterOS and return parsed results. Returns error on failure.

```go
result, err := client.Query([]string{"/interface/print"})
// []QueryResult{ { ".id": "*1", "name": "ether1", "type": "ether" } }
```

### `client.QueryContext(ctx, command)`

Query with Go context for cancellation/timeout.

```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

result, err := client.QueryContext(ctx, []string{"/ip/address/print"})
if err == context.DeadlineExceeded {
    fmt.Println("Query timed out")
}
```

### `client.QueryStream(ctx, command, onRow)`

Stream rows as they arrive from the server.

```go
rows, err := client.QueryStream(context.Background(), []string{"/ip/address/print"}, func(row routeros.QueryResult) {
    fmt.Println("Row:", row["address"])
})
```

### `client.QuerySafe(command)`

Never returns an error. Always returns `QuerySafeResult`.

```go
result := client.QuerySafe([]string{"/interface/print"})
if result.IsError {
    fmt.Println("Error:", result.Error.Message)
} else {
    fmt.Println("Interfaces:", result.Data)
}
```

### `client.QuerySafeContext(ctx, command)`

Context-aware safe variant.

### `client.GetStats()`

Returns pool statistics.

```go
stats := client.GetStats()
fmt.Printf("Queries: %d, Active: %d, Uptime: %dms\n",
    stats.TotalQueries, stats.ActiveConnections, stats.Uptime)
```

### Events

```go
client.On("send", func(data interface{}) {
    event := data.(map[string]interface{})
    fmt.Println("Sent:", event["id"], event["cmd"])
})

client.On("receive", func(data interface{}) {
    event := data.(map[string]interface{})
    fmt.Println("Received:", event["id"], event["data"])
})

client.On("row", func(data interface{}) {
    event := data.(map[string]interface{})
    fmt.Println("Row:", event["id"], event["data"])
})

client.On("error", func(data interface{}) {
    event := data.(map[string]interface{})
    fmt.Println("Error:", event["error"])
})
```

### Concurrent queries

The connection pool allows multiple queries to run concurrently:

```go
var wg sync.WaitGroup
for _, cmd := range commands {
    wg.Add(1)
    go func(c []string) {
        defer wg.Done()
        result, err := client.Query(c)
    }(cmd)
}
wg.Wait()
```

### `client.Close()`

Close all connections and clean up resources.

```go
defer client.Close()
```

## Error Handling

| Error Type              | Description                        |
|-------------------------|------------------------------------|
| `*RouterOSAPIError`     | Base error (all types)             |
| `*TimeoutError`         | Connection or query timeout        |
| `*AuthenticationError`  | Login failed (wrong credentials)   |
| `*ConnectionError`      | Connection refused, closed, etc.   |
| `*ProtocolError`        | RouterOS API returned an error trap|
| `*RetryError`           | All retry attempts failed          |

```go
import routeros "github.com/turgocloudyogya/routeros-api-go"

result, err := client.Query([]string{"/ip/address/print"})
if err != nil {
    switch e := err.(type) {
    case *routeros.TimeoutError:
        fmt.Println("Request timed out")
    case *routeros.AuthenticationError:
        fmt.Println("Bad credentials")
    case *routeros.ConnectionError:
        fmt.Println("Connection issue:", e.Message)
    case *routeros.ProtocolError:
        fmt.Println("API error:", e.Message, e.Detail)
    case *routeros.RetryError:
        fmt.Println("Retries exhausted:", e.Cause)
    default:
        fmt.Println("Error:", err)
    }
}
```

## How It Works

### Protocol

RouterOS API uses a simple word-based protocol over TCP:
- Each word is length-prefixed with a variable-length encoding (1–5 bytes)
- Commands are sent as arrays of words terminated by an empty word
- Responses use `!re`, `!trap`, and `!done` markers

### Queue System

Each connection maintains a FIFO queue. A command is sent only after the
previous command's `!done` response is received. This is required because
RouterOS processes commands sequentially per connection.

### Connection Pool

The pool maintains multiple connections (default: 3). Requests are distributed
across connections using round-robin. This allows parallel command execution.

## License

Apache-2.0
