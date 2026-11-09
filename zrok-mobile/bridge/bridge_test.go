package bridge

import (
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
