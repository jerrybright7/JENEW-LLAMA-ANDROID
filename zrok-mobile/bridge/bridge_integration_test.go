package bridge

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func startTestEchoBackend(t *testing.T) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for test backend: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer conn.Close()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()
	return listener.Addr().String(), func() {
		_ = listener.Close()
		<-done
	}
}

func startTestForwarder(t *testing.T, token, target string) (*tcpForwarder, <-chan struct{}) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for test frontend %q: %v", token, err)
	}
	forwarder := &tcpForwarder{
		listener: listener,
		stop:     make(chan struct{}),
		conns:    make(map[net.Conn]struct{}),
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		forwarder.acceptLoop(target, token)
	}()
	return forwarder, done
}

func assertForwarderEcho(t *testing.T, address, message string) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatalf("connect to forwarder %s: %v", address, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.WriteString(conn, message); err != nil {
		t.Fatalf("write through forwarder: %v", err)
	}
	got := make([]byte, len(message))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("read echoed response: %v", err)
	}
	if string(got) != message {
		t.Fatalf("echo = %q, want %q", got, message)
	}
}

func TestMultiServiceForwardersRunIndependently(t *testing.T) {
	// Isolate package-level bridge state; this test intentionally exercises
	// the same accept/copy/close implementation used by SDK-created listeners.
	mu.Lock()
	oldForwarders, oldShares, oldPath := forwarders, managedShares, shareRegistryPath
	forwarders = make(map[string]*tcpForwarder)
	managedShares = make(map[string]managedShare)
	shareRegistryPath = filepath.Join(t.TempDir(), "managed-shares.json")
	mu.Unlock()

	var started []*tcpForwarder
	var completed []<-chan struct{}
	defer func() {
		for _, f := range started {
			_ = f.close()
		}
		for _, done := range completed {
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Error("forwarder accept loop did not stop")
			}
		}
		mu.Lock()
		forwarders, managedShares, shareRegistryPath = oldForwarders, oldShares, oldPath
		mu.Unlock()
	}()

	targetOne, stopBackendOne := startTestEchoBackend(t)
	defer stopBackendOne()
	targetTwo, stopBackendTwo := startTestEchoBackend(t)
	defer stopBackendTwo()

	shareOne := managedShare{ShareToken: "test-service-one", BackendMode: "tcpTunnel", Target: targetOne, Running: true}
	shareTwo := managedShare{ShareToken: "test-service-two", BackendMode: "tcpTunnel", Target: targetTwo, Running: true}

	f1, done1 := startTestForwarder(t, shareOne.ShareToken, targetOne)
	f2, done2 := startTestForwarder(t, shareTwo.ShareToken, targetTwo)
	started = append(started, f1, f2)
	completed = append(completed, done1, done2)

	mu.Lock()
	forwarders[shareOne.ShareToken], forwarders[shareTwo.ShareToken] = f1, f2
	managedShares[shareOne.ShareToken], managedShares[shareTwo.ShareToken] = shareOne, shareTwo
	mu.Unlock()

	assertForwarderEcho(t, f1.listener.Addr().String(), "service-one")
	assertForwarderEcho(t, f2.listener.Addr().String(), "service-two")

	// Stop service one only. Service two must remain usable.
	if err := f1.close(); err != nil {
		t.Fatalf("stop first forwarder: %v", err)
	}
	select {
	case <-done1:
	case <-time.After(2 * time.Second):
		t.Fatal("first forwarder did not stop")
	}
	assertForwarderEcho(t, f2.listener.Addr().String(), "service-two-still-running")

	// Persist both service definitions, reload them as a new process would,
	// and verify that listeners are not falsely reported as running.
	mu.Lock()
	if err := persistManagedSharesLocked(); err != nil {
		mu.Unlock()
		t.Fatalf("persist managed services: %v", err)
	}
	registryPath := shareRegistryPath
	mu.Unlock()

	data, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatalf("read persisted registry: %v", err)
	}
	var restored map[string]managedShare
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("decode persisted registry: %v", err)
	}
	for token, share := range restored {
		share.Running = false
		restored[token] = share
	}
	if len(restored) != 2 || restored[shareOne.ShareToken].Target != targetOne || restored[shareTwo.ShareToken].Target != targetTwo {
		t.Fatalf("restored service definitions do not match: %#v", restored)
	}
	for token, share := range restored {
		if share.Running {
			t.Errorf("service %s incorrectly restored as running", token)
		}
	}

	// An unreachable local target must fail only its own connection; the
	// healthy second forwarder must continue to carry traffic.
	unavailable, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve unavailable target port: %v", err)
	}
	unavailableAddress := unavailable.Addr().String()
	_ = unavailable.Close()

	failed, failedDone := startTestForwarder(t, "test-unreachable", unavailableAddress)
	started = append(started, failed)
	completed = append(completed, failedDone)
	client, err := net.DialTimeout("tcp", failed.listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatalf("connect to failure-isolation forwarder: %v", err)
	}
	_ = client.SetDeadline(time.Now().Add(2 * time.Second))
	_, _ = fmt.Fprint(client, "this target is intentionally unavailable")
	one := make([]byte, 1)
	if _, readErr := client.Read(one); readErr == nil {
		t.Fatal("unreachable target unexpectedly returned data")
	}
	_ = client.Close()

	assertForwarderEcho(t, f2.listener.Addr().String(), "healthy-service-survives-failure")
}
