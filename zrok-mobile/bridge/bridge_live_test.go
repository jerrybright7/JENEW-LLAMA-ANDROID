package bridge

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/openziti/zrok/v2/sdk/golang/sdk"
)

func TestLiveZrokMultiServiceRoundTrip(t *testing.T) {
	if os.Getenv("ZROK_LIVE_TEST") != "1" {
		t.Skip("set ZROK_LIVE_TEST=1 to run the authenticated live zrok test")
	}
	apiEndpoint := os.Getenv("ZROK_LIVE_API_ENDPOINT")
	accountToken := os.Getenv("ZROK_LIVE_ACCOUNT_TOKEN")
	host := os.Getenv("ZROK_LIVE_HOST")
	if apiEndpoint == "" || accountToken == "" || host == "" {
		t.Fatal("live zrok test requires ZROK_LIVE_API_ENDPOINT, ZROK_LIVE_ACCOUNT_TOKEN, and ZROK_LIVE_HOST")
	}

	mu.Lock()
	if len(forwarders) != 0 {
		mu.Unlock()
		t.Fatal("live test requires an isolated Go test process with no active forwarders")
	}
	mu.Unlock()

	privateRoot := filepath.Join(t.TempDir(), "zrok2")
	if err := Initialize(privateRoot); err != nil {
		t.Fatalf("initialize isolated zrok root: %v", err)
	}
	if err := EnableEnvironment(apiEndpoint, accountToken, host, "JENEW live multi-service CI verification"); err != nil {
		t.Fatalf("authenticate and enable isolated zrok environment: %v", err)
	}

	var tokens []string
	var accesses []*sdk.Access
	var started []string
	defer func() {
		for _, token := range started {
			_ = StopTCPForwarder(token)
		}
		for _, access := range accesses {
			_ = sdk.DeleteAccess(root, access)
		}
		for _, token := range tokens {
			_ = DeleteShare(token)
		}
		_ = DisableEnvironment()
	}()

	targetOne, stopOne := startTestEchoBackend(t)
	defer stopOne()
	targetTwo, stopTwo := startTestEchoBackend(t)
	defer stopTwo()

	type createdShare struct {
		Token string `json:"token"`
	}
	createAndStart := func(target string) string {
		t.Helper()
		result, err := CreateShare("tcpTunnel", "public", target)
		if err != nil {
			t.Fatalf("create live zrok TCP share: %v", err)
		}
		var share createdShare
		if err := json.Unmarshal([]byte(result), &share); err != nil {
			t.Fatalf("decode created share response: %v", err)
		}
		if share.Token == "" {
			t.Fatal("zrok returned an empty share token")
		}
		tokens = append(tokens, share.Token)
		if err := StartTCPForwarder(share.Token, target); err != nil {
			t.Fatalf("start live TCP forwarder: %v", err)
		}
		started = append(started, share.Token)
		return share.Token
	}

	tokenOne := createAndStart(targetOne)
	tokenTwo := createAndStart(targetTwo)

	// A share token identifies the hosted share, but NewDialer requires the
	// frontend token returned by CreateAccess. Create one access per share.
	accessOne, err := sdk.CreateAccess(root, &sdk.AccessRequest{ShareToken: tokenOne})
	if err != nil {
		t.Fatalf("create access for first live share: %v", err)
	}
	accesses = append(accesses, accessOne)
	accessTwo, err := sdk.CreateAccess(root, &sdk.AccessRequest{ShareToken: tokenTwo})
	if err != nil {
		t.Fatalf("create access for second live share: %v", err)
	}
	accesses = append(accesses, accessTwo)

	// Dial through each access token to exercise the authenticated Ziti data plane.
	exchange := func(accessToken, message string) error {
		conn, err := sdk.NewDialer(accessToken, root)
		if err != nil {
			return fmt.Errorf("create zrok dialer: %w", err)
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
		if _, err := io.WriteString(conn, message); err != nil {
			return fmt.Errorf("write through live zrok share: %w", err)
		}
		got := make([]byte, len(message))
		if _, err := io.ReadFull(conn, got); err != nil {
			return fmt.Errorf("read through live zrok share: %w", err)
		}
		if string(got) != message {
			return fmt.Errorf("live zrok response = %q, want %q", got, message)
		}
		return nil
	}

	errs := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		errs <- exchange(accessOne.Token, "live-service-one")
	}()
	go func() {
		defer wg.Done()
		errs <- exchange(accessTwo.Token, "live-service-two")
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}

	// Stop one live service and prove the other remains usable.
	if err := StopTCPForwarder(tokenOne); err != nil {
		t.Fatalf("stop first live service: %v", err)
	}
	started = started[1:]
	if err := exchange(accessTwo.Token, "second-service-survives-first-stop"); err != nil {
		t.Fatalf("second live service failed after stopping first: %v", err)
	}

	// Check the persisted registry without printing tokens into CI logs.
	var states []managedShare
	if err := json.Unmarshal([]byte(ListManagedShares()), &states); err != nil {
		t.Fatalf("decode live managed-share registry: %v", err)
	}
	if len(states) != 2 {
		t.Fatalf("managed registry contains %d services, want 2", len(states))
	}
}
