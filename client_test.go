package mikrotik

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

const (
	testHost = "127.0.0.1"
	testPort = 5503
	testSSL  = 5504
	testUser = "admin"
	testPass = "admin"
)

func TestBasicConnect(t *testing.T) {
	client, err := NewClient(Config{
		Host: testHost, Port: testPort,
		Username: testUser, Password: testPass,
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Connect(); err != nil {
		t.Fatal(err)
	}
	t.Log("✓ Connected")
	client.Close()
}

func TestInvalidCredentials(t *testing.T) {
	client, err := NewClient(Config{
		Host: testHost, Port: testPort,
		Username: "admin", Password: "wrong",
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	err = client.Connect()
	if err == nil {
		client.Close()
		t.Fatal("expected error")
	}
	if _, ok := err.(*AuthenticationError); !ok {
		t.Fatalf("expected *AuthenticationError, got %T: %s", err, err)
	}
	t.Logf("✓ Rejected: %s", err)
	client.Close()
}

func TestQueryTimeout(t *testing.T) {
	client, err := NewClient(Config{
		Host: testHost, Port: testPort,
		Username: testUser, Password: testPass,
		Timeout: 5 * time.Second, QueryTimeout: 8 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Connect(); err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	r, err := client.Query([]string{"/system/identity/print"})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("✓ Identity: %v", r[0]["name"])
}

func TestReadQueries(t *testing.T) {
	client, err := NewClient(Config{
		Host: testHost, Port: testPort,
		Username: testUser, Password: testPass,
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Connect(); err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	r, err := client.Query([]string{"/ip/address/print"})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("✓ /ip/address/print → %d address(es)", len(r))
	for _, a := range r {
		t.Logf("  - %s", a["address"])
	}

	ifaces, err := client.Query([]string{"/interface/print"})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("✓ /interface/print → %d interface(s)", len(ifaces))

	ident, err := client.Query([]string{"/system/identity/print"})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("✓ /system/identity/print → %s", ident[0]["name"])

	ping, err := client.Query([]string{"/tool/ping", "=address=10.0.0.1", "=count=3"})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("✓ /tool/ping → %d result(s)", len(ping))
	for _, p := range ping {
		t.Logf("  - status: %s", p["status"])
	}
}

func TestSSLConnect(t *testing.T) {
	client, err := NewClient(Config{
		Host: testHost, Port: testSSL, SSL: true,
		Username: testUser, Password: testPass,
		Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Connect(); err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	r, err := client.Query([]string{"/system/identity/print"})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("✓ SSL identity: %s", r[0]["name"])
}

func TestCreateBridgeVLANsIPs(t *testing.T) {
	client, err := NewClient(Config{
		Host: testHost, Port: testPort,
		Username: testUser, Password: testPass,
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Connect(); err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	_, err = client.Query([]string{"/interface/bridge/add", "=name=test-bridge-1"})
	if err != nil {
		t.Fatal(err)
	}
	t.Log("✓ Bridge created")

	for i := 1; i <= 3; i++ {
		_, err := client.Query([]string{"/interface/vlan/add",
			fmt.Sprintf("=name=test-vlan-%d", i),
			fmt.Sprintf("=vlan-id=%d00", i),
			"=interface=veth0"})
		if err != nil {
			t.Fatalf("create vlan %d: %s", i, err)
		}
	}
	t.Log("✓ 3 VLANs created")

	for i := 1; i <= 3; i++ {
		_, err := client.Query([]string{"/ip/address/add",
			fmt.Sprintf("=address=10.20.%d.1/24", i),
			"=interface=veth0",
			fmt.Sprintf("=comment=test-ip-%d", i)})
		if err != nil {
			t.Fatalf("create address %d: %s", i, err)
		}
	}
	t.Log("✓ 3 IP addresses created")

	list, err := client.Query([]string{"/ip/address/print"})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range list {
		if a["comment"] != "" && a["comment"] != "false" {
			_, err := client.Query([]string{"/ip/address/remove", fmt.Sprintf("=.id=%s", a[".id"])})
			if err != nil {
				t.Logf("  cleanup addr %s: %s", a["address"], err)
			}
		}
	}
	t.Log("✓ IPs cleaned")

	for i := 1; i <= 3; i++ {
		_, err := client.Query([]string{"/interface/vlan/remove", fmt.Sprintf("=name=test-vlan-%d", i)})
		if err != nil {
			t.Logf("  cleanup vlan %d: %s", i, err)
		}
	}
	t.Log("✓ VLANs cleaned")

	_, err = client.Query([]string{"/interface/bridge/remove", "=name=test-bridge-1"})
	if err != nil {
		t.Logf("  cleanup bridge: %s", err)
	}
	t.Log("✓ Bridge cleaned")
}

func TestEventSendReceive(t *testing.T) {
	client, err := NewClient(Config{
		Host: testHost, Port: testPort,
		Username: testUser, Password: testPass,
		Timeout: 5 * time.Second, PoolSize: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Connect(); err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	sendCh := make(chan string, 10)
	recvCh := make(chan string, 10)

	client.On("send", func(data interface{}) {
		if e, ok := data.(map[string]interface{}); ok {
			if id, ok := e["id"].(string); ok {
				sendCh <- id
			}
		}
	})
	client.On("receive", func(data interface{}) {
		if e, ok := data.(map[string]interface{}); ok {
			if id, ok := e["id"].(string); ok {
				recvCh <- id
			}
		}
	})

	_, err = client.Query([]string{"/system/identity/print"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Query([]string{"/ip/address/print"})
	if err != nil {
		t.Fatal(err)
	}

	var sendIDs, recvIDs []string
	for i := 0; i < 2; i++ {
		select {
		case id := <-sendCh:
			sendIDs = append(sendIDs, id)
		case <-time.After(time.Second):
		}
		select {
		case id := <-recvCh:
			recvIDs = append(recvIDs, id)
		case <-time.After(time.Second):
		}
	}
	t.Logf("✓ Send events: %v", sendIDs)
	t.Logf("✓ Receive events: %v", recvIDs)
}

func TestPoolConcurrent(t *testing.T) {
	client, err := NewClient(Config{
		Host: testHost, Port: testPort,
		Username: testUser, Password: testPass,
		Timeout: 5 * time.Second, PoolSize: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Connect(); err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var wg sync.WaitGroup
	errCh := make(chan error, 5)
	cmds := [][]string{
		{"/ip/address/print"},
		{"/interface/print"},
		{"/system/identity/print"},
		{"/ip/route/print"},
		{"/tool/ping", "=address=10.0.0.1"},
	}
	for _, cmd := range cmds {
		wg.Add(1)
		go func(c []string) {
			defer wg.Done()
			_, err := client.Query(c)
			errCh <- err
		}(cmd)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Errorf("concurrent query error: %s", err)
		}
	}
	t.Log("✓ 5 concurrent queries completed")

	for i := 0; i < 20; i++ {
		_, err := client.Query([]string{"/system/identity/print"})
		if err != nil {
			t.Errorf("burst %d: %s", i, err)
		}
	}
	t.Log("✓ 20 burst queries completed")
}

func TestPoolSizeConfig(t *testing.T) {
	client, err := NewClient(Config{
		Host: testHost, Port: testPort,
		Username: testUser, Password: testPass,
		Timeout: 5 * time.Second, PoolSize: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Connect(); err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	t.Log("✓ Pool with 5 connections initialized")
	for i := 0; i < 10; i++ {
		_, err := client.Query([]string{"/system/identity/print"})
		if err != nil {
			t.Errorf("query %d: %s", i, err)
		}
	}
	t.Log("✓ 10 queries through pool completed")
}

func TestQueryAfterCloseFails(t *testing.T) {
	client, err := NewClient(Config{
		Host: testHost, Port: testPort,
		Username: testUser, Password: testPass,
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Connect(); err != nil {
		t.Fatal(err)
	}
	client.Close()
	_, err = client.Query([]string{"/system/identity/print"})
	if err == nil {
		t.Fatal("expected error after close")
	}
	if _, ok := err.(*ConnectionError); !ok {
		t.Fatalf("expected *ConnectionError, got %T: %s", err, err)
	}
	t.Logf("✓ Query after close rejected: %s", err)
}

func TestQuerySafe(t *testing.T) {
	client, err := NewClient(Config{
		Host: testHost, Port: testPort,
		Username: testUser, Password: testPass,
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Connect(); err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	result := client.QuerySafe([]string{"/system/identity/print"})
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.Error)
	}
	if len(result.Data) == 0 || result.Data[0]["name"] == "" {
		t.Fatal("no identity returned")
	}
	t.Logf("✓ QuerySafe success: %s", result.Data[0]["name"])

	errResult := client.QuerySafe([]string{"/invalid/command"})
	if !errResult.IsError {
		t.Fatal("expected error for invalid command")
	}
	t.Logf("✓ QuerySafe error: %s", errResult.Error)
}

func TestConcurrentQuerySafe(t *testing.T) {
	client, err := NewClient(Config{
		Host: testHost, Port: testPort,
		Username: testUser, Password: testPass,
		Timeout: 5 * time.Second, PoolSize: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Connect(); err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var wg sync.WaitGroup
	results := make([]QuerySafeResult, 4)
	cmds := [][]string{
		{"/ip/address/print"},
		{"/interface/print"},
		{"/system/identity/print"},
		{"/invalid/command"},
	}

	for i, cmd := range cmds {
		wg.Add(1)
		go func(idx int, c []string) {
			defer wg.Done()
			results[idx] = client.QuerySafe(c)
		}(i, cmd)
	}
	wg.Wait()

	successCount := 0
	errorCount := 0
	for _, r := range results {
		if r.IsError {
			errorCount++
		} else {
			successCount++
		}
	}
	if successCount != 3 || errorCount != 1 {
		t.Fatalf("expected 3 success + 1 error, got %d success + %d error", successCount, errorCount)
	}
	t.Logf("✓ Concurrent QuerySafe: %d success, %d error", successCount, errorCount)
}
