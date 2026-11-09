package bridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/openziti/zrok/v2/sdk/golang/sdk"
)

func TestBuildShareRequest(t *testing.T) {
	request, err := buildShareRequest("tcpTunnel", "public", "127.0.0.1:8080")
	if err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	if request.BackendMode != sdk.TcpTunnelBackendMode {
		t.Fatalf("backend = %q, want %q", request.BackendMode, sdk.TcpTunnelBackendMode)
	}
	if request.ShareMode != sdk.PublicShareMode {
		t.Fatalf("share mode = %q, want %q", request.ShareMode, sdk.PublicShareMode)
	}
	if request.Target != "127.0.0.1:8080" {
		t.Fatalf("target = %q", request.Target)
	}
}

func TestBuildShareRequestRejectsInvalidInput(t *testing.T) {
	cases := []struct {
		name, backend, mode, target string
	}{
		{"empty target", "tcpTunnel", "public", ""},
		{"invalid TCP target", "tcpTunnel", "public", "localhost"},
		{"invalid TCP port", "tcpTunnel", "public", "127.0.0.1:70000"},
		{"unknown backend", "shell", "public", "127.0.0.1:8080"},
		{"unknown share mode", "tcpTunnel", "internet", "127.0.0.1:8080"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := buildShareRequest(tc.backend, tc.mode, tc.target); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestValidateAPIEndpoint(t *testing.T) {
	for _, endpoint := range []string{
		"https://api-v2.zrok.io",
		"http://127.0.0.1:18080",
	} {
		if err := validateAPIEndpoint(endpoint); err != nil {
			t.Errorf("%q rejected: %v", endpoint, err)
		}
	}
	for _, endpoint := range []string{
		"",
		"api-v2.zrok.io",
		"ftp://api-v2.zrok.io",
		"https://user:pass@example.com",
		"https://example.com?token=secret",
	} {
		if err := validateAPIEndpoint(endpoint); err == nil {
			t.Errorf("%q unexpectedly accepted", endpoint)
		}
	}
}

func TestValidateTCPAddress(t *testing.T) {
	for _, address := range []string{
		"127.0.0.1:8080",
		"localhost:443",
		"[::1]:9000",
	} {
		if err := validateTCPAddress(address); err != nil {
			t.Errorf("%q rejected: %v", address, err)
		}
	}
	for _, address := range []string{
		"",
		"localhost",
		":8080",
		"localhost:0",
		"localhost:65536",
		"localhost:http",
	} {
		if err := validateTCPAddress(address); err == nil {
			t.Errorf("%q unexpectedly accepted", address)
		}
	}
}

func TestValidateDisablePreconditions(t *testing.T) {
	cases := []struct {
		name             string
		enabled          bool
		activeForwarders int
		wantError        bool
	}{
		{name: "enabled and idle", enabled: true, activeForwarders: 0},
		{name: "not enabled", enabled: false, activeForwarders: 0, wantError: true},
		{name: "active listener", enabled: true, activeForwarders: 1, wantError: true},
		{name: "multiple active listeners", enabled: true, activeForwarders: 3, wantError: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateDisablePreconditions(tc.enabled, tc.activeForwarders)
			if (err != nil) != tc.wantError {
				t.Fatalf("error = %v, wantError = %v", err, tc.wantError)
			}
		})
	}
}

func TestManagedShareRegistryPersistsMultipleServices(t *testing.T) {
	previousPath := shareRegistryPath
	previousShares := managedShares
	shareRegistryPath = filepath.Join(t.TempDir(), "managed-shares.json")
	managedShares = map[string]managedShare{
		"token-ssh": {
			ShareToken: "token-ssh", BackendMode: "tcpTunnel", ShareMode: "private",
			Target: "127.0.0.1:22", FrontendEndpoints: []string{"ssh.example.test"}, Running: true,
		},
		"token-web": {
			ShareToken: "token-web", BackendMode: "web", ShareMode: "public",
			Target: "http://127.0.0.1:8080", FrontendEndpoints: []string{"web.example.test"},
		},
	}
	defer func() {
		shareRegistryPath = previousPath
		managedShares = previousShares
	}()

	if err := persistManagedSharesLocked(); err != nil {
		t.Fatalf("persist registry: %v", err)
	}
	data, err := os.ReadFile(shareRegistryPath)
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	var loaded map[string]managedShare
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("decode registry: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("persisted %d services, want 2", len(loaded))
	}
	if loaded["token-ssh"].Target != "127.0.0.1:22" || loaded["token-web"].Target != "http://127.0.0.1:8080" {
		t.Fatalf("service records were not preserved: %#v", loaded)
	}
	if info, err := os.Stat(shareRegistryPath); err != nil {
		t.Fatalf("stat registry: %v", err)
	} else if info.Mode().Perm() != 0600 {
		t.Fatalf("registry permissions = %o, want 600", info.Mode().Perm())
	}
}

func TestListManagedSharesReturnsEveryService(t *testing.T) {
	previousShares := managedShares
	managedShares = map[string]managedShare{
		"token-one": {ShareToken: "token-one", BackendMode: "tcpTunnel", Target: "127.0.0.1:22"},
		"token-two": {ShareToken: "token-two", BackendMode: "web", Target: "http://127.0.0.1:8080"},
	}
	defer func() { managedShares = previousShares }()

	var listed []managedShare
	if err := json.Unmarshal([]byte(ListManagedShares()), &listed); err != nil {
		t.Fatalf("decode listed services: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("listed %d services, want 2", len(listed))
	}
}
