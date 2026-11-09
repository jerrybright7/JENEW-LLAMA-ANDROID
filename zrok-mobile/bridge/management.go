package bridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/go-openapi/runtime"
	httptransport "github.com/go-openapi/runtime/client"
	"github.com/openziti/zrok/v2/rest_client_zrok/metadata"
	"github.com/openziti/zrok/v2/rest_client_zrok/share"
)

func controllerAuthLocked() (runtime.ClientAuthInfoWriter, error) {
	if root == nil || !root.IsEnabled() {
		return nil, errors.New("zrok environment is not enabled")
	}
	return httptransport.APIKeyAuth("X-TOKEN", "header", root.Environment().AccountToken), nil
}

// ListShareNamespaces returns namespaces available to the enabled environment.
func ListShareNamespaces() (string, error) {
	mu.Lock()
	defer mu.Unlock()
	auth, err := controllerAuthLocked()
	if err != nil { return "", err }
	client, err := root.Client()
	if err != nil { return "", err }
	response, err := client.Share.ListShareNamespaces(share.NewListShareNamespacesParams(), auth)
	if err != nil { return "", fmt.Errorf("list zrok namespaces: %w", err) }
	data, err := json.Marshal(response.Payload)
	return string(data), err
}

// ListNamesForNamespace lists reserved names within one namespace.
func ListNamesForNamespace(namespaceToken string) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	auth, err := controllerAuthLocked()
	if err != nil { return "", err }
	namespaceToken = strings.TrimSpace(namespaceToken)
	if namespaceToken == "" { return "", errors.New("namespace token is required") }
	client, err := root.Client()
	if err != nil { return "", err }
	params := share.NewListNamesForNamespaceParams()
	params.NamespaceToken = namespaceToken
	response, err := client.Share.ListNamesForNamespace(params, auth)
	if err != nil { return "", fmt.Errorf("list names for namespace: %w", err) }
	data, err := json.Marshal(response.Payload)
	return string(data), err
}

// ListAllNames lists all reserved/named shares visible to the account.
func ListAllNames() (string, error) {
	mu.Lock()
	defer mu.Unlock()
	auth, err := controllerAuthLocked()
	if err != nil { return "", err }
	client, err := root.Client()
	if err != nil { return "", err }
	response, err := client.Share.ListAllNames(share.NewListAllNamesParams(), auth)
	if err != nil { return "", fmt.Errorf("list zrok named shares: %w", err) }
	data, err := json.Marshal(response.Payload)
	return string(data), err
}

// GetAccountMetrics returns controller-side account usage metrics as JSON.
// duration uses the zrok API duration syntax (for example, 1h, 24h, or 7d).
func GetAccountMetrics(duration string) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	auth, err := controllerAuthLocked()
	if err != nil { return "", err }
	client, err := root.Client()
	if err != nil { return "", err }
	params := metadata.NewGetAccountMetricsParams()
	if duration = strings.TrimSpace(duration); duration != "" { params.Duration = &duration }
	response, err := client.Metadata.GetAccountMetrics(params, auth)
	if err != nil { return "", fmt.Errorf("get zrok account metrics: %w", err) }
	data, err := json.Marshal(response.Payload)
	return string(data), err
}

// GetShareMetrics returns controller-side metrics for a share as JSON.
func GetShareMetrics(shareToken, duration string) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	auth, err := controllerAuthLocked()
	if err != nil { return "", err }
	shareToken = strings.TrimSpace(shareToken)
	if shareToken == "" { return "", errors.New("share token is required") }
	client, err := root.Client()
	if err != nil { return "", err }
	params := metadata.NewGetShareMetricsParams()
	params.ShareToken = shareToken
	if duration = strings.TrimSpace(duration); duration != "" { params.Duration = &duration }
	response, err := client.Metadata.GetShareMetrics(params, auth)
	if err != nil { return "", fmt.Errorf("get zrok share metrics: %w", err) }
	data, err := json.Marshal(response.Payload)
	return string(data), err
}
