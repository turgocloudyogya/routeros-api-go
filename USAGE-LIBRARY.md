# USAGE-LIBRARY: RouterOS API — Go

This file documents how AI agents should use the `routeros-api-go` library.
Install: `go get github.com/turgocloudyogya/routeros-api-go`

## Import

```go
import routeros "github.com/turgocloudyogya/routeros-api-go"
```

Types available:
- `*routeros.Client` — high-level client
- `routeros.Config` — configuration struct
- `routeros.SSLOptions`, `routeros.RetryConfig`, `routeros.HealthCheckConfig` — nested configs
- `routeros.QueryResult` — `map[string]interface{}` per `!re` row (values are strings by default, or auto-formatted numbers/bools)
- `routeros.QuerySafeResult` — `{IsError bool, Data []QueryResult, Error *RouterOSAPIError}`
- `routeros.PoolStats` — statistics struct
- Error types: `*RouterOSAPIError`, `*TimeoutError`, `*AuthenticationError`, `*ConnectionError`, `*ProtocolError`, `*RetryError`

## Instantiation

```go
// Minimal — all defaults apply
client, err := routeros.NewClient(routeros.Config{})

// Full config
client, err := routeros.NewClient(routeros.Config{
    Host:         "192.168.88.1",        // default: "192.168.88.1"
    Port:         8728,                   // default: 8728
    SSL:          false,                  // bool | SSLOptions
    Username:     "admin",               // default: "admin"
    Password:     "",                    // default: ""
    Timeout:      5 * time.Second,       // connect timeout, default: 5s
    QueryTimeout: 0,                     // per-query timeout, 0 = no timeout
    PoolSize:     3,                     // default: 3
    AutoConnect:  true,                  // connect async on NewClient
    IdleTimeout:  0,                     // close socket after idle, 0 = disabled
    AutoFormat:   false,                 // auto-convert "123"→123, "true"→true, etc.
    Retry: &routeros.RetryConfig{        // retry on failure, nil = no retry
        Retries:  3,
        MinDelay: 1 * time.Second,
        MaxDelay: 30 * time.Second,
    },
    HealthCheck: &routeros.HealthCheckConfig{ // periodic keep-alive, nil = disabled
        Interval: 30 * time.Second,
        Timeout:  5 * time.Second,       // optional
        Command:  []string{"/system/identity/print"}, // optional
    },
})

// SSL options
client, _ = routeros.NewClient(routeros.Config{
    SSL: true, // shortcut — SkipVerify: true

    SSL: routeros.SSLOptions{CA: "-----BEGIN CERTIFICATE-----\n...\n-----END CERTIFICATE-----", SkipVerify: false},

    SSL: routeros.SSLOptions{
        Cert: "-----BEGIN CERTIFICATE-----\n...\n-----END CERTIFICATE-----",
        Key:  "-----BEGIN PRIVATE KEY-----\n...\n-----END PRIVATE KEY-----",
        CA:   "-----BEGIN CERTIFICATE-----\n...\n-----END CERTIFICATE-----",
    },
})

if err != nil {
    log.Fatal(err)
}
defer client.Close()
```

## Auto-Format (`AutoFormat: true`)

When enabled, numeric strings and booleans are auto-converted in query results:

```go
client, _ := routeros.NewClient(routeros.Config{AutoFormat: true})

r, _ := client.Query([]string{"/interface/print"})
// Without AutoFormat: r[0]["running"] → "true" (string)
// With AutoFormat:    r[0]["running"] → true (bool)
//                     r[0]["mtu"] → 1500  (int64)
```

`QueryResult` values become `interface{}` — use type assertions or `fmt.Sprintf("%v", ...)`:

```go
r, _ := client.Query([]string{"/interface/print"})
name, _ := r[0]["name"].(string)        // always string
running, _ := r[0]["running"].(bool)    // only with AutoFormat
mtu, _ := r[0]["mtu"].(int64)           // only with AutoFormat
```

Auto-formatting skips IP addresses, CIDRs, and MAC addresses to preserve their string form.

## Core API Methods

### `client.Query(cmd)` / `client.QueryContext(ctx, cmd)` — returns error

```go
ifaces, err := client.Query([]string{"/interface/print"})
// ifaces is []routeros.QueryResult (map[string]interface{})
// Example: ifaces[0]["name"] → "ether1"

// With context for cancellation/timeout
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()
result, err := client.QueryContext(ctx, []string{"/tool/ping", "=address=10.0.0.1", "=count=100"})
if err == context.DeadlineExceeded {
    // query timed out
}
```

### `client.QuerySafe(cmd)` / `client.QuerySafeContext(ctx, cmd)` — never errors

```go
result := client.QuerySafe([]string{"/interface/print"})
if result.IsError {
    fmt.Println(result.Error.Message) // *RouterOSAPIError
} else {
    fmt.Println(result.Data) // []QueryResult
}
```

### `client.QueryStream(ctx, cmd, onRow)` — streaming

```go
rows, err := client.QueryStream(context.Background(), []string{"/ip/address/print"}, func(row routeros.QueryResult) {
    // called per !re sentence as it arrives
    fmt.Println("streamed:", row["address"])
})
// rows: accumulated results
```

### `client.Stats()` — pool statistics

```go
stats := client.Stats()
// routeros.PoolStats{
//     TotalConnections:     int,
//     ActiveConnections:    int,
//     DestroyedConnections: int,
//     QueuedTasks:          int,
//     TotalQueries:         int64,
//     FailedQueries:        int64,
//     Uptime:               time.Duration,
// }
```

### `client.Close()` — cleanup

```go
defer client.Close()
```

## Events

```go
client.On("send", func(data interface{}) {
    event := data.(map[string]interface{})
    fmt.Println("sent:", event["id"], event["cmd"])
})

client.On("receive", func(data interface{}) {
    event := data.(map[string]interface{})
    fmt.Println("received:", event["id"])
})

client.On("row", func(data interface{}) {
    event := data.(map[string]interface{})
    fmt.Println("row:", event["id"], event["data"])
})

client.On("error", func(data interface{}) {
    event := data.(map[string]interface{})
    fmt.Println("error:", event["error"])
})

client.On("connected", func(data interface{}) {})
client.On("disconnected", func(data interface{}) {})
```

## Command Format

```go
// Read
[]string{"/ip/address/print"}
[]string{"/interface/print"}
[]string{"/system/identity/print"}
[]string{"/tool/ping", "=address=10.0.0.1", "=count=3"}

// Write (mock returns nil, real router returns nil on success)
[]string{"/interface/bridge/add", "=name=my-bridge"}
[]string{"/interface/vlan/add", "=name=my-vlan", "=vlan-id=100", "=interface=ether1"}
[]string{"/ip/address/add", "=address=10.10.1.1/24", "=interface=bridge", "=comment=test"}
[]string{"/interface/bridge/remove", "=.id=*1"}
```

## Error Handling

```go
import routeros "github.com/turgocloudyogya/routeros-api-go"

result, err := client.Query([]string{"/ip/address/print"})
if err != nil {
    switch e := err.(type) {
    case *routeros.TimeoutError:
        // queryTimeout exceeded or connect timeout
    case *routeros.AuthenticationError:
        // wrong username/password
    case *routeros.ConnectionError:
        // connection refused, closed, destroyed
        fmt.Println(e.Message) // error string
    case *routeros.ProtocolError:
        // router returned !trap
        fmt.Println(e.Message, e.ID, e.Detail)
    case *routeros.RetryError:
        // all retries failed
        fmt.Println(e.Cause) // last error
    default:
        // base *RouterOSAPIError
    }
}

// Access error fields via type assertion to *RouterOSAPIError
if apiErr, ok := err.(*routeros.RouterOSAPIError); ok {
    fmt.Println(apiErr.Message, apiErr.ID)
}
```

## Patterns & Best Practices

### Auto-Connect

```go
client, _ := routeros.NewClient(routeros.Config{AutoConnect: true})
// pool.Init() runs in background goroutine
// Query/QuerySafe/QueryStream call pool.Init() synchronously if not yet initialized
result, err := client.Query([]string{"/system/identity/print"})
```

### Explicit Connect

```go
client, _ := routeros.NewClient(routeros.Config{AutoConnect: false})
if err := client.Connect(); err != nil {
    log.Fatal(err)
}
```

### Concurrent Queries

```go
var wg sync.WaitGroup
for i := 0; i < 10; i++ {
    wg.Add(1)
    go func() {
        defer wg.Done()
        result, err := client.Query([]string{"/system/identity/print"})
        _ = result
    }()
}
wg.Wait()
```

### Retry

```go
client, _ := routeros.NewClient(routeros.Config{
    Retry: &routeros.RetryConfig{
        Retries:  3,
        MinDelay: 1 * time.Second,
        MaxDelay: 10 * time.Second,
    },
})
// AuthenticationError is NOT retried; ConnectionError, ProtocolError ARE retried
```

### Health Checks

```go
client, _ := routeros.NewClient(routeros.Config{
    HealthCheck: &routeros.HealthCheckConfig{
        Interval: 30 * time.Second,
    },
})
// runs "/system/identity/print" every 30s via a goroutine
```

### Low-level Connection Usage

```go
// Direct connection (bypass pool)
conn := routeros.NewConnection(
    "192.168.88.1", 8728, nil,
    "admin", "password",
    5*time.Second, 0, 0,
)
if err := conn.Connect(); err != nil { log.Fatal(err) }
defer conn.Close()

result, err := conn.Execute([]string{"/system/identity/print"})
// or with context/streaming:
// conn.ExecuteContext(ctx, cmd)
// conn.ExecuteStream(ctx, cmd, onRow)

// Connection events
conn.On("send", func(data interface{}) {})
conn.On("receive", func(data interface{}) {})
conn.On("error", func(data interface{}) {
    m := data.(map[string]string)
    fmt.Println("error:", m["error"])
})
```

## Type Definitions Summary

```go
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
    AutoFormat   bool
    Retry        *RetryConfig
    HealthCheck  *HealthCheckConfig
}

type SSLOptions struct {
    Cert       string // PEM
    Key        string // PEM
    CA         string // PEM
    SkipVerify bool   // default true
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

type PoolStats struct {
    TotalConnections      int
    ActiveConnections     int
    DestroyedConnections  int
    QueuedTasks           int
    TotalQueries          int64
    FailedQueries         int64
    Uptime                time.Duration
}
```
