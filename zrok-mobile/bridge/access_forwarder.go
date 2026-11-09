package bridge

import (
	"errors"
	"fmt"
	"io"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/openziti/zrok/v2/sdk/golang/sdk"
)

type tcpAccessForwarder struct {
	listener net.Listener
	stop chan struct{}
	once sync.Once
	mu sync.Mutex
	closed bool
	conns map[net.Conn]struct{}
}

var accessForwarders = map[string]*tcpAccessForwarder{}

func (f *tcpAccessForwarder) track(conn net.Conn) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed { return false }
	f.conns[conn] = struct{}{}
	return true
}
func (f *tcpAccessForwarder) untrack(conn net.Conn) {
	f.mu.Lock()
	delete(f.conns, conn)
	f.mu.Unlock()
}
func (f *tcpAccessForwarder) close() error {
	var err error
	f.once.Do(func() {
		f.mu.Lock()
		f.closed = true
		close(f.stop)
		active := make([]net.Conn, 0, len(f.conns))
		for conn := range f.conns { active = append(active, conn) }
		f.mu.Unlock()
		err = f.listener.Close()
		for _, conn := range active { _ = conn.Close() }
	})
	return err
}

// StartTCPAccessForwarder exposes a remote private TCP share on a local
// loopback address. The access grant is retained so the forwarder can be
// stopped and restarted without losing the authorization record.
func StartTCPAccessForwarder(shareToken, bindAddress string) error {
	shareToken = strings.TrimSpace(shareToken)
	bindAddress = strings.TrimSpace(bindAddress)
	if shareToken == "" { return errors.New("share token is required") }
	if err := validateTCPAddress(bindAddress); err != nil { return fmt.Errorf("invalid local bind address: %w", err) }

	mu.Lock()
	if root == nil || !root.IsEnabled() { mu.Unlock(); return errors.New("zrok environment is not enabled") }
	if err := ensureAccessRegistryLocked(); err != nil { mu.Unlock(); return err }
	var accessToken string
	for token, record := range managedAccesses {
		if record.ShareToken == shareToken { accessToken = token; break }
	}
	mu.Unlock()

	if accessToken == "" {
		grant, err := CreateAccessGrant(shareToken, bindAddress)
		if err != nil { return err }
		// The result is an internal JSON response; do not log it.
		var decoded struct { AccessToken string `json:"accessToken"` }
		if err := json.Unmarshal([]byte(grant), &decoded); err != nil { return fmt.Errorf("decode private access grant: %w", err) }
		accessToken = decoded.AccessToken
	}

	listener, err := net.Listen("tcp", bindAddress)
	if err != nil { return fmt.Errorf("bind local private-access endpoint: %w", err) }
	f := &tcpAccessForwarder{listener: listener, stop: make(chan struct{}), conns: map[net.Conn]struct{}{}}
	backendMu.Lock()
	if accessForwarders[shareToken] != nil {
		backendMu.Unlock()
		_ = listener.Close()
		return errors.New("private access forwarder is already running")
	}
	accessForwarders[shareToken] = f
	backendMu.Unlock()

	mu.Lock()
	record, ok := managedAccesses[accessToken]
	if !ok {
		mu.Unlock()
		backendMu.Lock()
		delete(accessForwarders, shareToken)
		backendMu.Unlock()
		_ = f.close()
		return errors.New("private access grant disappeared before forwarding started")
	}
	record.BindAddress = bindAddress
	record.AutoStart = true
	managedAccesses[accessToken] = record
	persistErr := persistAccessesLocked()
	mu.Unlock()
	if persistErr != nil {
		backendMu.Lock()
		delete(accessForwarders, shareToken)
		backendMu.Unlock()
		_ = f.close()
		return persistErr
	}

	go func() {
		defer func() {
			_ = f.close()
			backendMu.Lock()
			if accessForwarders[shareToken] == f { delete(accessForwarders, shareToken) }
			backendMu.Unlock()
		}()
		for {
			local, acceptErr := listener.Accept()
			if acceptErr != nil {
				select { case <-f.stop: return; default: return }
			}
			if !f.track(local) { _ = local.Close(); return }
			go func(local net.Conn) {
				defer func() { _ = local.Close(); f.untrack(local) }()
				mu.Lock()
				currentRoot := root
				mu.Unlock()
				if currentRoot == nil { return }
				remote, dialErr := sdk.NewDialer(shareToken, currentRoot)
				if dialErr != nil { return }
				defer remote.Close()
				if !f.track(remote) { return }
				defer f.untrack(remote)
				done := make(chan struct{}, 2)
				go func() { _, _ = io.Copy(remote, local); done <- struct{}{} }()
				go func() { _, _ = io.Copy(local, remote); done <- struct{}{} }()
				select {
				case <-done:
				case <-f.stop:
				case <-time.After(24 * time.Hour):
				}
				_ = local.Close()
				_ = remote.Close()
				<-done
			}(local)
		}
	}()
	return nil
}

// StopTCPAccessForwarder stops local listening and closes active private-share
// connections. It leaves the access grant intact for a future restart.
func StopTCPAccessForwarder(shareToken string) error {
	shareToken = strings.TrimSpace(shareToken)
	backendMu.Lock()
	f := accessForwarders[shareToken]
	if f != nil { delete(accessForwarders, shareToken) }
	backendMu.Unlock()
	if f == nil { return errors.New("no private access forwarder is running for this share") }
	err := f.close()
	mu.Lock()
	for token, record := range managedAccesses {
		if record.ShareToken == shareToken {
			record.AutoStart = false
			managedAccesses[token] = record
		}
	}
	persistErr := persistAccessesLocked()
	mu.Unlock()
	if err != nil { return err }
	return persistErr
}

func ActiveAccessForwarderCount() int32 {
	backendMu.Lock()
	defer backendMu.Unlock()
	return int32(len(accessForwarders))
}


// AutoStartCount reports how many saved share/access engines are configured
// for restoration after an Android process restart.
func AutoStartCount() int32 {
	mu.Lock()
	defer mu.Unlock()
	count := int32(0)
	for _, share := range managedShares {
		if share.AutoStart { count++ }
	}
	if ensureAccessRegistryLocked() == nil {
		for _, access := range managedAccesses {
			if access.AutoStart { count++ }
		}
	}
	return count
}

// RestoreManagedBackends reopens the share and private-access listeners that
// were active before the Android process was restarted.
func RestoreManagedBackends() error {
	mu.Lock()
	if root == nil || !root.IsEnabled() {
		mu.Unlock()
		return errors.New("zrok environment is not enabled")
	}
	shares := make([]managedShare, 0)
	for _, share := range managedShares {
		if share.AutoStart { shares = append(shares, share) }
	}
	if err := ensureAccessRegistryLocked(); err != nil {
		mu.Unlock()
		return err
	}
	accesses := make([]managedAccess, 0)
	for _, access := range managedAccesses {
		if access.AutoStart { accesses = append(accesses, access) }
	}
	mu.Unlock()

	var failures []string
	for _, share := range shares {
		if err := StartShareBackend(share.ShareToken, share.BackendMode, share.Target); err != nil {
			failures = append(failures, fmt.Sprintf("%s backend: %v", share.BackendMode, err))
		}
	}
	for _, access := range accesses {
		if err := StartTCPAccessForwarder(access.ShareToken, access.BindAddress); err != nil {
			failures = append(failures, fmt.Sprintf("private access: %v", err))
		}
	}
	if len(failures) > 0 { return fmt.Errorf("some zrok engines could not be restored: %s", strings.Join(failures, "; ")) }
	return nil
}
