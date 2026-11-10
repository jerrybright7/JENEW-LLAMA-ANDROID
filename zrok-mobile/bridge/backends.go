package bridge

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/openziti/zrok/v2/endpoints/drive"
	"github.com/openziti/zrok/v2/endpoints/proxy"
	"github.com/openziti/zrok/v2/endpoints/socks"
	"github.com/openziti/zrok/v2/endpoints/tcpTunnel"
	"github.com/openziti/zrok/v2/endpoints/udpTunnel"
	"github.com/openziti/zrok/v2/sdk/golang/sdk"
)

type runnableBackend interface {
	Run() error
	Stop() error
}

type runningBackend struct {
	backend runnableBackend
	mode    string
}

var (
	backendMu sync.Mutex
	backends = map[string]*runningBackend{}
	caddyBackendToken string
)

// StartShareBackend starts the actual zrok-side backend for an existing share.
// Supported modes: tcpTunnel, udpTunnel, proxy, web, drive, socks, and caddy.
// For caddy, target is a Caddyfile path; for web/drive it is a local directory;
// proxy takes an upstream URL; TCP/UDP take host:port; SOCKS ignores target.
func StartShareBackend(shareToken, backendMode, target string) error {
	shareToken = strings.TrimSpace(shareToken)
	backendMode = strings.TrimSpace(backendMode)
	target = strings.TrimSpace(target)
	if shareToken == "" {
		return errors.New("share token is required")
	}

	mu.Lock()
	if root == nil || !root.IsEnabled() {
		mu.Unlock()
		return errors.New("zrok environment is not enabled")
	}
	identityPath, err := root.ZitiIdentityNamed(root.EnvironmentIdentityName())
	if err != nil {
		mu.Unlock()
		return fmt.Errorf("locate zrok identity: %w", err)
	}
	share, managed := managedShares[shareToken]
	if !managed {
		mu.Unlock()
		return errors.New("share is not managed by this app")
	}
	if share.BackendMode != "" && share.BackendMode != backendMode {
		mu.Unlock()
		return fmt.Errorf("share backend is %q, not %q", share.BackendMode, backendMode)
	}
	mu.Unlock()

	backendMu.Lock()
	if _, exists := backends[shareToken]; exists {
		backendMu.Unlock()
		return errors.New("backend is already running for this share")
	}
	if (backendMode == "web" || backendMode == "caddy") && caddyBackendToken != "" {
		backendMu.Unlock()
		return errors.New("another Caddy-backed share is running; stop it first")
	}
	backendMu.Unlock()

	var backend runnableBackend
	switch backendMode {
	case "tcpTunnel":
		if err := validateTCPAddress(target); err != nil { return err }
		backend, err = tcpTunnel.NewBackend(&tcpTunnel.BackendConfig{IdentityPath: identityPath, EndpointAddress: target, ShrToken: shareToken})
	case "udpTunnel":
		if err := validateTCPAddress(target); err != nil { return err }
		backend, err = udpTunnel.NewBackend(&udpTunnel.BackendConfig{IdentityPath: identityPath, EndpointAddress: target, ShrToken: shareToken})
	case "proxy":
		if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
			return errors.New("proxy target must be an http:// or https:// upstream URL")
		}
		backend, err = proxy.NewBackend(&proxy.BackendConfig{IdentityPath: identityPath, EndpointAddress: target, ShrToken: shareToken})
	case "web":
		if !filepath.IsAbs(target) { return errors.New("web root must be an absolute app-private directory path") }
		webInfo, statErr := os.Stat(target)
		if statErr != nil || !webInfo.IsDir() { return errors.New("web root must exist and be a directory") }
		backend, err = proxy.NewCaddyWebBackend(&proxy.CaddyWebBackendConfig{IdentityPath: identityPath, WebRoot: target, ShrToken: shareToken})
	case "drive":
		if !filepath.IsAbs(target) { return errors.New("drive root must be an absolute app-private directory path") }
		driveInfo, statErr := os.Stat(target)
		if statErr != nil || !driveInfo.IsDir() { return errors.New("drive root must exist and be a directory") }
		backend, err = drive.NewBackend(&drive.BackendConfig{IdentityPath: identityPath, DriveRoot: target, ShrToken: shareToken})
	case "socks":
		backend, err = socks.NewBackend(&socks.BackendConfig{IdentityPath: identityPath, ShrToken: shareToken})
	case "caddy":
		if !filepath.IsAbs(target) { return errors.New("Caddyfile path must be absolute") }
		caddyInfo, statErr := os.Stat(target)
		if statErr != nil || !caddyInfo.Mode().IsRegular() { return errors.New("Caddyfile must exist and be a regular file") }
		backend, err = proxy.NewCaddyfileBackend(&proxy.CaddyfileBackendConfig{
			CaddyfilePath: target, Shr: &sdk.Share{Token: shareToken},
		})
	default:
		return fmt.Errorf("unsupported backend engine %q", backendMode)
	}
	if err != nil {
		return fmt.Errorf("initialize %s backend: %s", backendMode, strings.ReplaceAll(err.Error(), shareToken, "[redacted]"))
	}

	backendMu.Lock()
	if _, exists := backends[shareToken]; exists {
		backendMu.Unlock()
		_ = backend.Stop()
		return errors.New("backend was started concurrently")
	}
	backends[shareToken] = &runningBackend{backend: backend, mode: backendMode}
	if backendMode == "web" || backendMode == "caddy" { caddyBackendToken = shareToken }
	backendMu.Unlock()

	mu.Lock()
	share.Running = true
	share.AutoStart = true
	managedShares[shareToken] = share
	persistErr := persistManagedSharesLocked()
	mu.Unlock()
	if persistErr != nil {
		_ = StopShareBackend(shareToken)
		return persistErr
	}

	clearRuntimeError()
	go func() {
		runErr := backend.Run()
		// Caddy's Run starts its managed servers and returns after startup.
		if backendMode == "web" || backendMode == "caddy" {
			if runErr != nil {
				cleanupErr := StopShareBackend(shareToken)
				message := backendMode + " backend failed: " + strings.ReplaceAll(runErr.Error(), shareToken, "[redacted]")
				if cleanupErr != nil { message += "; cleanup failed: " + strings.ReplaceAll(cleanupErr.Error(), shareToken, "[redacted]") }
				recordRuntimeError(message)
			}
			return
		}
		backendMu.Lock()
		current := backends[shareToken]
		if current == nil || current.backend != backend {
			backendMu.Unlock()
			return // a newer engine owns this share; ignore this stale completion
		}
		delete(backends, shareToken)
		backendMu.Unlock()
		mu.Lock()
		if managed, ok := managedShares[shareToken]; ok {
			managed.Running = false
			managed.AutoStart = false
			managedShares[shareToken] = managed
			_ = persistManagedSharesLocked()
		}
		mu.Unlock()
		if runErr != nil {
			recordRuntimeError(backendMode + " backend stopped unexpectedly: " + strings.ReplaceAll(runErr.Error(), shareToken, "[redacted]"))
		}
	}()
	return nil
}

// StopShareBackend stops the active backend engine for one share.
func StopShareBackend(shareToken string) error {
	shareToken = strings.TrimSpace(shareToken)
	backendMu.Lock()
	entry := backends[shareToken]
	if entry == nil {
		backendMu.Unlock()
		return errors.New("no backend engine is running for this share")
	}
	delete(backends, shareToken)
	if caddyBackendToken == shareToken { caddyBackendToken = "" }
	backendMu.Unlock()

	err := entry.backend.Stop()
	if err != nil {
		// Keep a failed-to-stop engine visible so the UI does not claim it is
		// stopped while its listener may still be serving traffic.
		backendMu.Lock()
		if backends[shareToken] == nil {
			backends[shareToken] = entry
			if entry.mode == "web" || entry.mode == "caddy" { caddyBackendToken = shareToken }
		}
		backendMu.Unlock()
		recordRuntimeError("could not stop " + entry.mode + " backend: " + strings.ReplaceAll(err.Error(), shareToken, "[redacted]"))
		return fmt.Errorf("stop %s backend: %w", entry.mode, err)
	}
	mu.Lock()
	if share, ok := managedShares[shareToken]; ok {
		share.Running = false
		share.AutoStart = false
		managedShares[shareToken] = share
		_ = persistManagedSharesLocked()
	}
	mu.Unlock()
	clearRuntimeError()
	return nil
}

// ActiveBackendCount reports the number of live backend engines.
func backendIsRunning(shareToken string) bool {
	backendMu.Lock()
	defer backendMu.Unlock()
	return backends[shareToken] != nil || accessForwarders[shareToken] != nil
}

func ActiveBackendCount() int32 {
	backendMu.Lock()
	defer backendMu.Unlock()
	return int32(len(backends) + len(accessForwarders))
}

// ActiveShareBackendCount reports only the actual zrok share-serving engines.
func ActiveShareBackendCount() int32 {
	backendMu.Lock()
	defer backendMu.Unlock()
	return int32(len(backends))
}
