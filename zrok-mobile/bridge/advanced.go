package bridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/openziti/zrok/v2/sdk/golang/sdk"
)

// ShareOptionsJSON is decoded inside Go so the mobile API can expose the full
// SDK ShareRequest surface without binding Go structs through gomobile.
type shareOptionsJSON struct {
	BackendMode       string   `json:"backendMode"`
	ShareMode         string   `json:"shareMode"`
	Target            string   `json:"target"`
	PrivateShareToken string   `json:"privateShareToken,omitempty"`
	BasicAuth         []string `json:"basicAuth,omitempty"`
	OAuthProvider     string   `json:"oauthProvider,omitempty"`
	OAuthEmailDomains []string `json:"oauthEmailDomains,omitempty"`
	OAuthRefreshSecs  int64    `json:"oauthRefreshSeconds,omitempty"`
	PermissionMode    string   `json:"permissionMode,omitempty"`
	AccessGrants      []string `json:"accessGrants,omitempty"`
	NameSelections    []string `json:"nameSelections,omitempty"`
}

type managedAccess struct {
	ShareToken  string `json:"shareToken"`
	AccessToken string `json:"accessToken"`
	BackendMode string `json:"backendMode"`
	BindAddress string `json:"bindAddress,omitempty"`
	AutoStart   bool   `json:"autoStart,omitempty"`
	CreatedAt   string `json:"createdAt"`
}

var (
	accessRegistryPath string
	managedAccesses = map[string]managedAccess{}
)

// CreateShareWithOptions accepts a JSON object containing the complete zrok
// SDK share options: backend/share mode, target, private-share token, Basic
// Auth, OAuth provider/domain restrictions, permission mode, access grants,
// and namespace/name selections. Keep the returned share token private.
func CreateShareWithOptions(optionsJSON string) (string, error) {
	var options shareOptionsJSON
	if err := json.Unmarshal([]byte(optionsJSON), &options); err != nil {
		return "", fmt.Errorf("decode zrok share options: %w", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if root == nil || !root.IsEnabled() {
		return "", errors.New("zrok environment is not enabled")
	}
	request, err := buildShareRequest(options.BackendMode, options.ShareMode, options.Target)
	if err != nil {
		return "", err
	}
	request.PrivateShareToken = strings.TrimSpace(options.PrivateShareToken)
	request.BasicAuth = options.BasicAuth
	request.OauthProvider = strings.TrimSpace(options.OAuthProvider)
	request.OauthEmailAddressPatterns = options.OAuthEmailDomains
	if options.OAuthRefreshSecs > 0 {
		request.OauthRefreshInterval = time.Duration(options.OAuthRefreshSecs) * time.Second
	}
	permission := strings.TrimSpace(options.PermissionMode)
	if permission == "" {
		permission = string(sdk.OpenPermissionMode)
	}
	if permission != string(sdk.OpenPermissionMode) && permission != string(sdk.ClosedPermissionMode) {
		return "", errors.New("permissionMode must be 'open' or 'closed'")
	}
	request.PermissionMode = sdk.PermissionMode(permission)
	request.AccessGrants = options.AccessGrants
	for _, selection := range options.NameSelections {
		parsed, parseErr := sdk.ParseNameSelection(selection)
		if parseErr != nil || strings.TrimSpace(parsed.NamespaceToken) == "" {
			return "", fmt.Errorf("invalid namespace/name selection %q", selection)
		}
		request.NameSelections = append(request.NameSelections, parsed)
	}
	created, err := sdk.CreateShare(root, request)
	if err != nil {
		return "", fmt.Errorf("create configured zrok share: %w", err)
	}
	encoded, err := json.Marshal(created)
	if err != nil {
		_ = sdk.DeleteShare(root, created)
		return "", fmt.Errorf("encode created share: %w", err)
	}
	managedShares[created.Token] = managedShare{
		ShareToken: created.Token, BackendMode: options.BackendMode,
		ShareMode: options.ShareMode, Target: options.Target,
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

func ensureAccessRegistryLocked() error {
	if accessRegistryPath != "" {
		return nil
	}
	if shareRegistryPath == "" {
		return errors.New("initialize zrok before managing private accesses")
	}
	accessRegistryPath = filepath.Join(filepath.Dir(shareRegistryPath), "managed-accesses.json")
	data, err := os.ReadFile(accessRegistryPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read private access registry: %w", err)
	}
	if err == nil {
		if err := json.Unmarshal(data, &managedAccesses); err != nil {
			return fmt.Errorf("decode private access registry: %w", err)
		}
	}
	if managedAccesses == nil {
		managedAccesses = map[string]managedAccess{}
	}
	return nil
}

func persistAccessesLocked() error {
	if err := ensureAccessRegistryLocked(); err != nil {
		return err
	}
	data, err := json.Marshal(managedAccesses)
	if err != nil {
		return fmt.Errorf("encode private access registry: %w", err)
	}
	tmp := accessRegistryPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("write private access registry: %w", err)
	}
	if err := os.Chmod(tmp, 0600); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("secure private access registry: %w", err)
	}
	if err := os.Rename(tmp, accessRegistryPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("commit private access registry: %w", err)
	}
	return nil
}

// CreateAccessGrant registers this environment as an authorized accessor for
// a private share. The returned access token is sensitive and must stay local.
func CreateAccessGrant(shareToken, bindAddress string) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	if root == nil || !root.IsEnabled() {
		return "", errors.New("zrok environment is not enabled")
	}
	shareToken = strings.TrimSpace(shareToken)
	if shareToken == "" {
		return "", errors.New("share token is required")
	}
	if err := ensureAccessRegistryLocked(); err != nil {
		return "", err
	}
	access, err := sdk.CreateAccess(root, &sdk.AccessRequest{
		ShareToken: shareToken, BindAddress: strings.TrimSpace(bindAddress),
	})
	if err != nil {
		return "", fmt.Errorf("create zrok private access: %s", strings.ReplaceAll(err.Error(), shareToken, "[redacted]"))
	}
	managedAccesses[access.Token] = managedAccess{
		ShareToken: shareToken, AccessToken: access.Token,
		BackendMode: string(access.BackendMode),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := persistAccessesLocked(); err != nil {
		_ = sdk.DeleteAccess(root, access)
		delete(managedAccesses, access.Token)
		return "", err
	}
	result, err := json.Marshal(map[string]string{
		"shareToken": shareToken, "accessToken": access.Token,
		"backendMode": string(access.BackendMode),
	})
	if err != nil {
		return "", err
	}
	return string(result), nil
}

// ListManagedAccesses returns private access records, including sensitive
// frontend tokens needed for cleanup. Never log or export this response.
func ListManagedAccesses() string {
	mu.Lock()
	defer mu.Unlock()
	if err := ensureAccessRegistryLocked(); err != nil {
		return "[]"
	}
	records := make([]managedAccess, 0, len(managedAccesses))
	for _, access := range managedAccesses {
		records = append(records, access)
	}
	data, err := json.Marshal(records)
	if err != nil {
		return "[]"
	}
	return string(data)
}

// DeleteAccessGrant revokes a previously-created private access grant.
func DeleteAccessGrant(accessToken string) error {
	mu.Lock()
	defer mu.Unlock()
	if root == nil || !root.IsEnabled() {
		return errors.New("zrok environment is not enabled")
	}
	accessToken = strings.TrimSpace(accessToken)
	record, exists := managedAccesses[accessToken]
	if !exists {
		return errors.New("private access grant is not managed by this app")
	}
	if err := sdk.DeleteAccess(root, &sdk.Access{
		Token: record.AccessToken, ShareToken: record.ShareToken,
		BackendMode: sdk.BackendMode(record.BackendMode),
	}); err != nil {
		return fmt.Errorf("revoke zrok private access: %s", strings.ReplaceAll(err.Error(), accessToken, "[redacted]"))
	}
	delete(managedAccesses, accessToken)
	return persistAccessesLocked()
}

// GetShareDetail returns controller metadata for a share as JSON. Treat
// returned metadata as private because it can contain identifiers and tokens.
func GetShareDetail(shareToken string) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	if root == nil || !root.IsEnabled() {
		return "", errors.New("zrok environment is not enabled")
	}
	shareToken = strings.TrimSpace(shareToken)
	if shareToken == "" {
		return "", errors.New("share token is required")
	}
	detail, err := sdk.GetShareDetail(root, shareToken)
	if err != nil {
		return "", fmt.Errorf("get zrok share detail: %s", strings.ReplaceAll(err.Error(), shareToken, "[redacted]"))
	}
	data, err := json.Marshal(detail)
	if err != nil {
		return "", fmt.Errorf("encode zrok share detail: %w", err)
	}
	return string(data), nil
}
