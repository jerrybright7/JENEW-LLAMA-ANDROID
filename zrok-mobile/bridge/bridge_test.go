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
