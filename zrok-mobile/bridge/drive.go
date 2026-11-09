package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/openziti/zrok/v2/drives/davClient"
	"github.com/openziti/zrok/v2/sdk/golang/sdk"
)

func driveClientForShare(shareToken string) (*davClient.Client, func(), error) {
	shareToken = strings.TrimSpace(shareToken)
	if shareToken == "" { return nil, nil, errors.New("drive share token is required") }
	mu.Lock()
	if root == nil || !root.IsEnabled() { mu.Unlock(); return nil, nil, errors.New("zrok environment is not enabled") }
	if err := ensureAccessRegistryLocked(); err != nil { mu.Unlock(); return nil, nil, err }
	hasGrant := false
	for _, record := range managedAccesses {
		if record.ShareToken == shareToken { hasGrant = true; break }
	}
	currentRoot := root
	mu.Unlock()
	if !hasGrant {
		if _, err := CreateAccessGrant(shareToken, ""); err != nil { return nil, nil, err }
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return sdk.NewDialer(shareToken, currentRoot)
		},
	}
	httpClient := &http.Client{Transport: transport}
	client, err := davClient.NewClient(httpClient, "http://zrok/")
	if err != nil { transport.CloseIdleConnections(); return nil, nil, fmt.Errorf("initialize zrok drive client: %w", err) }
	return client, transport.CloseIdleConnections, nil
}

func driveLocalPath(relative string) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	if root == nil || root.Metadata() == nil { return "", errors.New("zrok app-private storage is not initialized") }
	relative = strings.TrimSpace(relative)
	if relative == "" || filepath.IsAbs(relative) { return "", errors.New("local drive file path must be a non-empty relative path") }
	clean := filepath.Clean(relative)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", errors.New("local drive path cannot escape its private files directory")
	}
	base := filepath.Join(root.Metadata().RootPath, "drive-files")
	path := filepath.Join(base, clean)
	rel, err := filepath.Rel(base, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", errors.New("local drive path escapes the private files directory")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil { return "", err }
	return path, nil
}

// DriveList lists a zrok Drive directory and returns file metadata as JSON.
func DriveList(shareToken, remotePath string, recursive bool) (string, error) {
	client, closeTransport, err := driveClientForShare(shareToken)
	if err != nil { return "", err }
	defer closeTransport()
	items, err := client.Readdir(context.Background(), remotePath, recursive)
	if err != nil { return "", fmt.Errorf("list zrok drive: %w", err) }
	data, err := json.Marshal(items)
	if err != nil { return "", err }
	return string(data), nil
}

// DriveDownload copies a remote Drive file into the app-private drive-files directory.
func DriveDownload(shareToken, remotePath, localRelativePath string) error {
	client, closeTransport, err := driveClientForShare(shareToken)
	if err != nil { return err }
	defer closeTransport()
	path, err := driveLocalPath(localRelativePath)
	if err != nil { return err }
	reader, err := client.Open(context.Background(), remotePath)
	if err != nil { return fmt.Errorf("open remote drive file: %w", err) }
	defer reader.Close()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil { return err }
	_, copyErr := io.Copy(file, reader)
	closeErr := file.Close()
	if copyErr != nil { return fmt.Errorf("download drive file: %w", copyErr) }
	if closeErr != nil { return closeErr }
	return nil
}

// DriveUpload copies an app-private drive-files file to a remote Drive path.
func DriveUpload(shareToken, localRelativePath, remotePath string) error {
	client, closeTransport, err := driveClientForShare(shareToken)
	if err != nil { return err }
	defer closeTransport()
	path, err := driveLocalPath(localRelativePath)
	if err != nil { return err }
	file, err := os.Open(path)
	if err != nil { return fmt.Errorf("open local drive file: %w", err) }
	defer file.Close()
	writer, err := client.Create(context.Background(), remotePath)
	if err != nil { return fmt.Errorf("create remote drive file: %w", err) }
	_, copyErr := io.Copy(writer, file)
	closeErr := writer.Close()
	if copyErr != nil { return fmt.Errorf("upload drive file: %w", copyErr) }
	if closeErr != nil { return closeErr }
	return nil
}

func DriveMkdir(shareToken, remotePath string) error {
	client, closeTransport, err := driveClientForShare(shareToken)
	if err != nil { return err }
	defer closeTransport()
	return client.Mkdir(context.Background(), remotePath)
}

func DriveRemove(shareToken, remotePath string) error {
	client, closeTransport, err := driveClientForShare(shareToken)
	if err != nil { return err }
	defer closeTransport()
	return client.RemoveAll(context.Background(), remotePath)
}

func DriveCopy(shareToken, sourcePath, destinationPath string, overwrite bool) error {
	client, closeTransport, err := driveClientForShare(shareToken)
	if err != nil { return err }
	defer closeTransport()
	return client.CopyAll(context.Background(), sourcePath, destinationPath, overwrite)
}

func DriveMove(shareToken, sourcePath, destinationPath string, overwrite bool) error {
	client, closeTransport, err := driveClientForShare(shareToken)
	if err != nil { return err }
	defer closeTransport()
	return client.MoveAll(context.Background(), sourcePath, destinationPath, overwrite)
}
