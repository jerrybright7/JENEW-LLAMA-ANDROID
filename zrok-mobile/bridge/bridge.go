package bridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/openziti/zrok/v2/environment/env_core"
	"github.com/openziti/zrok/v2/environment/env_v0_4"
	"github.com/openziti/zrok/v2/sdk/golang/sdk"
)

var (
	mu   sync.Mutex
	root *env_v0_4.Root
)

// Initialize points zrok's on-disk environment at a directory owned by the
// Android application (normally Context.getFilesDir()/zrok2). The caller must
// never pass shared/external storage. The SDK writes credentials and identity
// material below this directory with restrictive file permissions.
func Initialize(privateAppDirectory string) error {
	mu.Lock()
	defer mu.Unlock()

	privateAppDirectory = strings.TrimSpace(privateAppDirectory)
	if privateAppDirectory == "" || !filepath.IsAbs(privateAppDirectory) {
		return errors.New("zrok root must be a non-empty absolute app-private path")
	}
	privateAppDirectory = filepath.Clean(privateAppDirectory)
	if err := os.MkdirAll(privateAppDirectory, 0700); err != nil {
		return fmt.Errorf("create private zrok directory: %w", err)
	}
	if err := os.Chmod(privateAppDirectory, 0700); err != nil {
		return fmt.Errorf("secure private zrok directory: %w", err)
	}

	// env_v0_4 stores this as a package-level setting, so every operation is
	// serialized by mu and uses the same app-private root.
	env_v0_4.SetRootDirName(privateAppDirectory)
	loaded, err := env_v0_4.Load()
	if err != nil {
		return fmt.Errorf("load zrok environment: %w", err)
	}
	root = loaded
	return nil
}

// EnableEnvironment authenticates with an existing zrok account token,
// provisions the Ziti environment, and stores its identity in the SDK's
// expected "environment" identity slot.
func EnableEnvironment(apiEndpoint, accountToken, host, description string) error {
	mu.Lock()
	defer mu.Unlock()

	if root == nil {
		return errors.New("initialize zrok with the Android app-private directory first")
	}
	if root.IsEnabled() {
		return errors.New("zrok environment is already enabled")
	}
	apiEndpoint = strings.TrimSpace(apiEndpoint)
	accountToken = strings.TrimSpace(accountToken)
	host = strings.TrimSpace(host)
	description = strings.TrimSpace(description)
	if err := validateAPIEndpoint(apiEndpoint); err != nil {
		return err
	}
	if accountToken == "" {
		return errors.New("zrok account token is required")
	}
	if host == "" {
		return errors.New("zrok environment host is required")
	}
	if description == "" {
		description = "JENEW Android"
	}

	cfg := &env_core.Config{ApiEndpoint: apiEndpoint}
	if err := root.SetConfig(cfg); err != nil {
		return fmt.Errorf("save zrok API configuration: %w", err)
	}
	// The v2 SDK reads the account token from the pre-enable environment.
	if err := root.SetEnvironment(&env_core.Environment{
		AccountToken: accountToken,
		ApiEndpoint:  apiEndpoint,
	}); err != nil {
		return fmt.Errorf("save zrok account configuration: %w", err)
	}

	enabled, err := sdk.EnableEnvironment(root, &sdk.EnableRequest{
		Host:        host,
		Description: description,
	})
	if err != nil {
		return fmt.Errorf("enable zrok environment: %w", err)
	}

	// Listener and dialer resolve the identity from this named file. Do not
	// omit this step: the identity string in environment.json is not the
	// identity configuration file itself.
	if err := root.SaveZitiIdentityNamed(root.EnvironmentIdentityName(), enabled.ZitiConfig); err != nil {
		_ = sdk.DisableEnvironment(enabled, root)
		_ = root.DeleteEnvironment()
		return fmt.Errorf("save zrok Ziti identity: %w", err)
	}
	if err := root.SetEnvironment(&env_core.Environment{
		AccountToken: accountToken,
		ZitiIdentity: enabled.ZitiIdentity,
		ApiEndpoint:  apiEndpoint,
	}); err != nil {
		_ = root.DeleteZitiIdentityNamed(root.EnvironmentIdentityName())
		_ = sdk.DisableEnvironment(enabled, root)
		_ = root.DeleteEnvironment()
		return fmt.Errorf("persist enabled zrok environment: %w", err)
	}
	return nil
}

// Status returns non-secret environment state as JSON. It intentionally never
// returns the account token, Ziti identity contents, or filesystem paths.
func Status() string {
	mu.Lock()
	defer mu.Unlock()

	state := map[string]interface{}{
		"initialized": root != nil,
		"enabled":     false,
		"identitySaved": false,
	}
	if root != nil {
		state["enabled"] = root.IsEnabled()
		if root.IsEnabled() {
			state["apiEndpoint"] = root.Environment().ApiEndpoint
			identityPath, err := root.ZitiIdentityNamed(root.EnvironmentIdentityName())
			if err == nil {
				if info, statErr := os.Stat(identityPath); statErr == nil && info.Mode().IsRegular() {
					state["identitySaved"] = true
				}
			}
		}
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return "{\"initialized\":false,\"enabled\":false,\"error\":\"status encoding failed\"}"
	}
	return string(encoded)
}

// CreateShare creates a real zrok share and returns its token and frontend
// endpoints as JSON. Store the returned token securely in the Android app.
func CreateShare(backendMode, shareMode, target string) (string, error) {
	mu.Lock()
	defer mu.Unlock()

	if root == nil {
		return "", errors.New("zrok is not initialized")
	}
	if !root.IsEnabled() {
		return "", errors.New("zrok environment is not enabled")
	}
	request, err := buildShareRequest(backendMode, shareMode, target)
	if err != nil {
		return "", err
	}
	created, err := sdk.CreateShare(root, request)
	if err != nil {
		return "", fmt.Errorf("create zrok share: %w", err)
	}
	encoded, err := json.Marshal(created)
	if err != nil {
		_ = sdk.DeleteShare(root, created)
		return "", fmt.Errorf("encode created share: %w", err)
	}
	return string(encoded), nil
}

// DeleteShare removes a previously-created share by its share token.
func DeleteShare(shareToken string) error {
	mu.Lock()
	defer mu.Unlock()

	if root == nil || !root.IsEnabled() {
		return errors.New("zrok environment is not enabled")
	}
	shareToken = strings.TrimSpace(shareToken)
	if shareToken == "" {
		return errors.New("share token is required")
	}
	if err := sdk.DeleteShare(root, &sdk.Share{Token: shareToken}); err != nil {
		return fmt.Errorf("delete zrok share: %w", err)
	}
	return nil
}

func buildShareRequest(backendMode, shareMode, target string) (*sdk.ShareRequest, error) {
	backendMode = strings.TrimSpace(backendMode)
	shareMode = strings.TrimSpace(shareMode)
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, errors.New("share target is required")
	}

	backend := sdk.BackendMode(backendMode)
	switch backend {
	case sdk.ProxyBackendMode, sdk.WebBackendMode, sdk.TcpTunnelBackendMode,
		sdk.UdpTunnelBackendMode, sdk.CaddyBackendMode, sdk.DriveBackendMode,
		sdk.SocksBackendMode:
	default:
		return nil, fmt.Errorf("unsupported zrok backend mode %q", backendMode)
	}

	mode := sdk.ShareMode(shareMode)
	if mode != sdk.PublicShareMode && mode != sdk.PrivateShareMode {
		return nil, fmt.Errorf("unsupported zrok share mode %q", shareMode)
	}

	return &sdk.ShareRequest{
		BackendMode: backend,
		ShareMode:   mode,
		Target:      target,
	}, nil
}

func validateAPIEndpoint(endpoint string) error {
	parsed, err := url.ParseRequestURI(endpoint)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return errors.New("zrok API endpoint must be an absolute http:// or https:// URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("zrok API endpoint must not contain credentials, query parameters, or a fragment")
	}
	return nil
}

// SDKCompileProbe remains a packaging smoke-test entry point. It does not
// authenticate or contact a zrok controller.
func SDKCompileProbe() string {
	_ = sdk.CreateShare
	_ = sdk.DeleteShare
	_ = sdk.NewListener
	_ = sdk.NewDialer
	request, _ := buildShareRequest(
		string(sdk.TcpTunnelBackendMode),
		string(sdk.PublicShareMode),
		"127.0.0.1:8080",
	)
	return fmt.Sprintf("zrok SDK v2 bridge ready; backend=%s; mode=%s",
		request.BackendMode, request.ShareMode)
}
