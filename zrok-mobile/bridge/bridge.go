package bridge

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/openziti/zrok/v2/environment/env_core"
	"github.com/openziti/zrok/v2/environment/env_v0_4"
	"github.com/openziti/zrok/v2/sdk/golang/sdk"
)

var (
	mu                sync.Mutex
	root              *env_v0_4.Root
	forwarders        = map[string]*tcpForwarder{}
	managedShares     = map[string]managedShare{}
	shareRegistryPath string
)

type managedShare struct {
	ShareToken       string   `json:"shareToken"`
	BackendMode      string   `json:"backendMode"`
	ShareMode        string   `json:"shareMode"`
	Target           string   `json:"target"`
	FrontendEndpoints []string `json:"frontendEndpoints,omitempty"`
	Running          bool     `json:"running"`
	CreatedAt        string   `json:"createdAt"`
}

func persistManagedSharesLocked() error {
	if shareRegistryPath == "" {
		return errors.New("zrok share registry is not initialized")
	}
	data, err := json.Marshal(managedShares)
	if err != nil {
		return fmt.Errorf("encode zrok share registry: %w", err)
	}
	tmp := shareRegistryPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("write zrok share registry: %w", err)
	}
	if err := os.Chmod(tmp, 0600); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("secure zrok share registry: %w", err)
	}
	if err := os.Rename(tmp, shareRegistryPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("commit zrok share registry: %w", err)
	}
	return nil
}

func setManagedShareRunningLocked(token string, running bool) {
	share, exists := managedShares[token]
	if !exists || share.Running == running {
		return
	}
	share.Running = running
	managedShares[token] = share
	_ = persistManagedSharesLocked()
}

type tcpForwarder struct {
	listener net.Listener
	stop     chan struct{}
	once     sync.Once
	connMu   sync.Mutex
	closed   bool
	conns    map[net.Conn]struct{}
	closeErr error
}

func (f *tcpForwarder) track(conn net.Conn) bool {
	f.connMu.Lock()
	defer f.connMu.Unlock()
	if f.closed {
		return false
	}
	f.conns[conn] = struct{}{}
	return true
}

func (f *tcpForwarder) untrack(conn net.Conn) {
	f.connMu.Lock()
	delete(f.conns, conn)
	f.connMu.Unlock()
}

func (f *tcpForwarder) close() error {
	f.once.Do(func() {
		f.connMu.Lock()
		f.closed = true
		close(f.stop)
		active := make([]net.Conn, 0, len(f.conns))
		for conn := range f.conns {
			active = append(active, conn)
		}
		f.connMu.Unlock()

		f.closeErr = f.listener.Close()
		for _, conn := range active {
			_ = conn.Close()
		}
	})
	return f.closeErr
}

func (f *tcpForwarder) acceptLoop(localAddress, shareToken string) {
	defer func() {
		_ = f.close()
		mu.Lock()
		if forwarders[shareToken] == f {
			delete(forwarders, shareToken)
		}
		setManagedShareRunningLocked(shareToken, false)
		mu.Unlock()
	}()

	for {
		remote, err := f.listener.Accept()
		if err != nil {
			select {
			case <-f.stop:
				return
			default:
			}
			return
		}
		if !f.track(remote) {
			_ = remote.Close()
			return
		}
		go f.forwardConnection(remote, localAddress)
	}
}

func (f *tcpForwarder) forwardConnection(remote net.Conn, localAddress string) {
	defer func() {
		_ = remote.Close()
		f.untrack(remote)
	}()

	local, err := net.DialTimeout("tcp", localAddress, 10*time.Second)
	if err != nil {
		return
	}
	if !f.track(local) {
		_ = local.Close()
		return
	}
	defer func() {
		_ = local.Close()
		f.untrack(local)
	}()

	done := make(chan struct{}, 2)
	copyStream := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		if closeWriter, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = closeWriter.CloseWrite()
		}
		done <- struct{}{}
	}
	go copyStream(local, remote)
	go copyStream(remote, local)

	<-done
	_ = remote.Close()
	_ = local.Close()
	<-done
}

func validateTCPAddress(address string) error {
	host, portText, err := net.SplitHostPort(strings.TrimSpace(address))
	if err != nil || strings.TrimSpace(host) == "" {
		return errors.New("local TCP target must be a host:port address")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return errors.New("local TCP target port must be between 1 and 65535")
	}
	return nil
}

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
	if root != nil && root.Metadata() != nil {
		if root.Metadata().RootPath != privateAppDirectory && len(forwarders) > 0 {
			return errors.New("cannot switch zrok storage roots while TCP forwarders are active")
		}
		if root.Metadata().RootPath == privateAppDirectory && len(forwarders) > 0 {
			return nil
		}
	}
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
	shareRegistryPath = filepath.Join(privateAppDirectory, "managed-shares.json")
	managedShares = map[string]managedShare{}
	registryData, readErr := os.ReadFile(shareRegistryPath)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return fmt.Errorf("read zrok share registry: %w", readErr)
	}
	if readErr == nil {
		if err := json.Unmarshal(registryData, &managedShares); err != nil {
			return fmt.Errorf("decode zrok share registry: %w", err)
		}
	}
	if managedShares == nil {
		managedShares = map[string]managedShare{}
	}
	// Process restarts close listeners. Retain service definitions, but only
	// report a service running after its forwarder has been recreated.
	for token, share := range managedShares {
		share.Running = false
		managedShares[token] = share
	}
	if err := persistManagedSharesLocked(); err != nil {
		return err
	}
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
		// The pre-enable environment exists only to supply the account token
		// to the SDK request. Remove it on failure so a retry is possible.
		_ = root.DeleteEnvironment()
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

// DisableEnvironment asks the zrok controller to disable this environment,
// then removes the local identity and credential-bearing environment file.
// It refuses to run while listeners are active so callers cannot strand
// forwarding goroutines or invalidate their identity underneath them.
func DisableEnvironment() error {
	mu.Lock()
	defer mu.Unlock()

	if err := validateDisablePreconditions(root != nil && root.IsEnabled(), len(forwarders)); err != nil {
		return err
	}

	environment := root.Environment()
	if err := sdk.DisableEnvironment(&sdk.Environment{
		ZitiIdentity: environment.ZitiIdentity,
	}, root); err != nil {
		return fmt.Errorf("disable zrok environment: %w", err)
	}

	identityPath, err := root.ZitiIdentityNamed(root.EnvironmentIdentityName())
	if err != nil {
		return fmt.Errorf("locate local zrok identity: %w", err)
	}
	if err := os.Remove(identityPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove local zrok identity after remote disable: %w", err)
	}
	if err := root.DeleteEnvironment(); err != nil {
		return fmt.Errorf("remove local zrok credentials after remote disable: %w", err)
	}
	managedShares = map[string]managedShare{}
	if shareRegistryPath != "" {
		if err := os.Remove(shareRegistryPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove local zrok share registry: %w", err)
		}
	}
	return nil
}

func validateDisablePreconditions(enabled bool, activeForwarders int) error {
	if !enabled {
		return errors.New("zrok environment is not enabled")
	}
	if activeForwarders != 0 {
		return errors.New("stop all active TCP forwarders before disabling zrok")
	}
	return nil
}

// Status returns non-secret environment state as JSON. It intentionally never
// returns the account token, Ziti identity contents, or filesystem paths.
func Status() string {
	mu.Lock()
	defer mu.Unlock()

	state := map[string]interface{}{
		"initialized":      root != nil,
		"enabled":          false,
		"identitySaved":    false,
		"activeForwarders": len(forwarders),
		"managedShares":    len(managedShares),
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
	managedShares[created.Token] = managedShare{
		ShareToken: created.Token, BackendMode: strings.TrimSpace(backendMode),
		ShareMode: strings.TrimSpace(shareMode), Target: strings.TrimSpace(target),
		FrontendEndpoints: created.FrontendEndpoints,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := persistManagedSharesLocked(); err != nil {
		delete(managedShares, created.Token)
		_ = sdk.DeleteShare(root, created)
		return "", err
	}
	return string(encoded), nil
}

// ListManagedShares returns locally managed services as JSON. Share tokens are
// included for bridge operations; callers must keep this response private.
func ListManagedShares() string {
	mu.Lock()
	defer mu.Unlock()
	shares := make([]managedShare, 0, len(managedShares))
	for _, share := range managedShares {
		shares = append(shares, share)
	}
	encoded, err := json.Marshal(shares)
	if err != nil {
		return "[]"
	}
	return string(encoded)
}


// StartTCPForwarder accepts connections arriving through a zrok share and
 // forwards each connection to a TCP service reachable from this Android
 // device (for example, 127.0.0.1:8080). Use Status() to inspect the active
 // forwarder count and StopTCPForwarder() to stop it.
func StartTCPForwarder(shareToken, localAddress string) error {
	mu.Lock()
	defer mu.Unlock()

	if root == nil || !root.IsEnabled() {
		return errors.New("zrok environment is not enabled")
	}
	shareToken = strings.TrimSpace(shareToken)
	if shareToken == "" {
		return errors.New("share token is required")
	}
	if err := validateTCPAddress(localAddress); err != nil {
		return err
	}
	if _, exists := forwarders[shareToken]; exists {
		return errors.New("a TCP forwarder for this share is already running")
	}

	listener, err := sdk.NewListener(shareToken, root)
	if err != nil {
		return fmt.Errorf("start zrok share listener: %w", err)
	}
	forwarder := &tcpForwarder{
		listener: listener,
		stop:     make(chan struct{}),
		conns:    make(map[net.Conn]struct{}),
	}
	forwarders[shareToken] = forwarder
	setManagedShareRunningLocked(shareToken, true)
	if err := persistManagedSharesLocked(); err != nil {
		delete(forwarders, shareToken)
		setManagedShareRunningLocked(shareToken, false)
		_ = forwarder.close()
		return err
	}
	go forwarder.acceptLoop(localAddress, shareToken)
	return nil
}

// TestPrivateShare creates a temporary private access grant and performs an HTTP
// GET through the zrok data plane. It returns only the HTTP status and response
// body; share/access tokens are never returned or logged.
func TestPrivateShare(shareToken string) (string, error) {
	mu.Lock()
	defer mu.Unlock()

	if root == nil || !root.IsEnabled() {
		return "", errors.New("zrok environment is not enabled")
	}
	shareToken = strings.TrimSpace(shareToken)
	if shareToken == "" {
		return "", errors.New("share token is required")
	}

	access, err := sdk.CreateAccess(root, &sdk.AccessRequest{ShareToken: shareToken})
	if err != nil {
		return "", fmt.Errorf("create private-share access grant: %w", err)
	}
	defer func() { _ = sdk.DeleteAccess(root, access) }()

	conn, err := sdk.NewDialer(shareToken, root)
	if err != nil {
		return "", fmt.Errorf("create private-share dialer: %w", err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(25 * time.Second)); err != nil {
		return "", fmt.Errorf("set private-share test deadline: %w", err)
	}
	if _, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"); err != nil {
		return "", fmt.Errorf("send HTTP request through private share: %w", err)
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodGet})
	if err != nil {
		return "", fmt.Errorf("read HTTP response through private share: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4096))
	if err != nil {
		return "", fmt.Errorf("read private-share response body: %w", err)
	}
	return fmt.Sprintf("HTTP %s\n\n%s", response.Status, strings.TrimSpace(string(body))), nil
}

// StopTCPForwarder stops accepting connections and closes active forwarded
// connections for the supplied share token.
func StopTCPForwarder(shareToken string) error {
	shareToken = strings.TrimSpace(shareToken)
	if shareToken == "" {
		return errors.New("share token is required")
	}
	mu.Lock()
	forwarder, exists := forwarders[shareToken]
	if exists {
		delete(forwarders, shareToken)
	}
	mu.Unlock()
	if !exists {
		return errors.New("no TCP forwarder is running for this share")
	}
	err := forwarder.close()
	mu.Lock()
	setManagedShareRunningLocked(shareToken, false)
	persistErr := persistManagedSharesLocked()
	mu.Unlock()
	if err != nil {
		return err
	}
	return persistErr
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
	if _, active := forwarders[shareToken]; active {
		return errors.New("stop the TCP forwarder before deleting its share")
	}
	if err := sdk.DeleteShare(root, &sdk.Share{Token: shareToken}); err != nil {
		return fmt.Errorf("delete zrok share: %w", err)
	}
	delete(managedShares, shareToken)
	if err := persistManagedSharesLocked(); err != nil {
		return err
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
	if backendMode == string(sdk.TcpTunnelBackendMode) {
		if err := validateTCPAddress(target); err != nil {
			return nil, fmt.Errorf("invalid TCP tunnel target: %w", err)
		}
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
	_ = ListManagedShares
	request, _ := buildShareRequest(
		string(sdk.TcpTunnelBackendMode),
		string(sdk.PublicShareMode),
		"127.0.0.1:8080",
	)
	return fmt.Sprintf("zrok SDK v2 bridge ready; backend=%s; mode=%s",
		request.BackendMode, request.ShareMode)
}
