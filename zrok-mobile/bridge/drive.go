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
	"time"

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
	httpClient := &http.Client{Transport: transport, Timeout: 2 * time.Minute}
	client, err := davClient.NewClient(httpClient, "http://zrok/")
	if err != nil { transport.CloseIdleConnections(); return nil, nil, fmt.Errorf("initialize zrok drive client: %w", err) }
	return client, transport.CloseIdleConnections, nil
}

func driveLocalPath(relative string) (string, error) {
	mu.Lock()
	if root == nil || root.Metadata() == nil { mu.Unlock(); return "", errors.New("zrok app-private storage is not initialized") }
	rootPath := root.Metadata().RootPath
	mu.Unlock()
	relative = strings.TrimSpace(relative)
	if relative == "" || filepath.IsAbs(relative) { return "", errors.New("local drive file path must be a non-empty relative path") }
	clean := filepath.Clean(relative)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", errors.New("local drive path cannot escape its private files directory")
	}
	base := filepath.Join(rootPath, "drive-files")
	if err := os.MkdirAll(base, 0700); err != nil { return "", fmt.Errorf("create private Drive directory: %w", err) }
	baseReal, err := filepath.EvalSymlinks(base)
	if err != nil { return "", fmt.Errorf("resolve private Drive directory: %w", err) }
	if filepath.Clean(baseReal) != filepath.Clean(base) { return "", errors.New("private Drive directory must not be a symbolic link") }
	path := filepath.Join(baseReal, clean)
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0700); err != nil { return "", fmt.Errorf("create private Drive subdirectory: %w", err) }
	parentReal, err := filepath.EvalSymlinks(parent)
	if err != nil { return "", fmt.Errorf("resolve private Drive subdirectory: %w", err) }
	rel, err := filepath.Rel(baseReal, parentReal)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", errors.New("local Drive path escapes the private files directory through a symbolic link")
	}
	resolved := filepath.Join(parentReal, filepath.Base(path))
	if info, statErr := os.Lstat(resolved); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("local Drive file path must not be a symbolic link")
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) { return "", statErr }
	return resolved, nil
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
	tmp, err := os.CreateTemp(filepath.Dir(path), ".jenew-drive-download-*")
	if err != nil { return fmt.Errorf("create temporary Drive download: %w", err) }
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0600); err != nil { _ = tmp.Close(); return err }
	_, copyErr := io.Copy(tmp, reader)
	closeErr := tmp.Close()
	if copyErr != nil { return fmt.Errorf("download Drive file: %w", copyErr) }
	if closeErr != nil { return fmt.Errorf("close downloaded Drive file: %w", closeErr) }
	if err := os.Rename(tmpPath, path); err != nil { return fmt.Errorf("commit downloaded Drive file: %w", err) }
	return nil
}

// DriveUpload copies an app-private drive-files file to a remote Drive path.
func DriveUpload(shareToken, localRelativePath, remotePath string) error {
	client, closeTransport, err := driveClientForShare(shareToken)
	if err != nil { return err }
	defer closeTransport()
	path, err := driveLocalPath(localRelativePath)
	if err != nil { return err }
	info, err := os.Lstat(path)
	if err != nil { return fmt.Errorf("inspect local Drive file: %w", err) }
	if !info.Mode().IsRegular() { return errors.New("local Drive upload source must be a regular file, not a symbolic link or special file") }
	file, err := os.Open(path)
	if err != nil { return fmt.Errorf("open local Drive file: %w", err) }
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
