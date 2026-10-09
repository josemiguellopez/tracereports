package tracereports

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const reportVersion = "0.2.0"
const reportReleases = "https://github.com/josemiguellopez/tracereports/releases/download"
const maxReportBinary = 256 << 20

func reportBinary(ctx context.Context) (string, error) {
	if explicit := getenv("BIN"); explicit != "" {
		return explicit, nil
	}
	if installed, err := exec.LookPath("tracereports"); err == nil {
		return installed, nil
	}
	if (runtime.GOOS != "windows" && runtime.GOOS != "linux" && runtime.GOOS != "darwin") || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		return "", fmt.Errorf("unsupported report platform")
	}
	base := ""
	if runtime.GOOS == "windows" {
		base = os.Getenv("LOCALAPPDATA")
	} else if runtime.GOOS != "darwin" {
		base = os.Getenv("XDG_CACHE_HOME")
	}
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".cache")
		if runtime.GOOS == "darwin" { base = filepath.Join(home, "Library", "Caches") }
	}
	parent := filepath.Join(base, "tracereports", reportVersion)
	target := filepath.Join(parent, runtime.GOOS+"_"+runtime.GOARCH)
	name, extension := "tracereports", ".tar.gz"
	if runtime.GOOS == "windows" {
		name, extension = name+".exe", ".zip"
	}
	binary := filepath.Join(target, name)
	cached := func() bool {
		info, err := os.Lstat(binary)
		if err != nil || !info.Mode().IsRegular() {
			return false
		}
		data, err := os.ReadFile(binary)
		if err != nil {
			return false
		}
		digest, err := os.ReadFile(filepath.Join(target, "binary.sha256"))
		return err == nil && fmt.Sprintf("%x", sha256.Sum256(data)) == strings.TrimSpace(string(digest))
	}
	if cached() {
		return binary, nil
	}
	if getenv("BIN_DOWNLOAD") == "0" {
		return "", fmt.Errorf("binary download disabled")
	}
	if err := os.MkdirAll(parent, 0700); err != nil {
		return "", err
	}
	lock := filepath.Join(parent, runtime.GOOS+"_"+runtime.GOARCH+".lock")
	for {
		if cached() {
			return binary, nil
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		err := os.Mkdir(lock, 0700)
		if err == nil {
			break
		}
		if !os.IsExist(err) {
			return "", err
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	defer os.Remove(lock)
	if cached() {
		return binary, nil
	}
	baseURL := getenv("BIN_BASE_URL")
	if baseURL == "" {
		baseURL = reportReleases
	}
	if baseURL != reportReleases {
		u, err := url.Parse(baseURL)
		if err != nil || u.Scheme != "http" || (u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" && u.Hostname() != "::1") || u.User != nil {
			return "", fmt.Errorf("BIN_BASE_URL must be a loopback test server")
		}
	}
	asset := "tracereports_" + reportVersion + "_" + runtime.GOOS + "_" + runtime.GOARCH + extension
	download := func(name string, limit int64) ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/v"+reportVersion+"/"+name, nil)
		if err != nil {
			return nil, err
		}
		res, err := (&http.Client{}).Do(req)
		if err != nil {
			return nil, err
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("release download HTTP %d", res.StatusCode)
		}
		return readReportBytes(res.Body, limit)
	}
	checksums, err := download("checksums.txt", 1<<20)
	if err != nil {
		return "", err
	}
	var expected string
	for _, line := range strings.Split(string(checksums), "\n") {
		parts := strings.Fields(line)
		if len(parts) == 2 && strings.TrimPrefix(parts[1], "*") == asset {
			if expected != "" {
				return "", fmt.Errorf("ambiguous release checksum")
			}
			expected = strings.ToLower(parts[0])
		}
	}
	if len(expected) != 64 || strings.Trim(expected, "0123456789abcdef") != "" {
		return "", fmt.Errorf("release checksum missing")
	}
	archive, err := download(asset, 128<<20)
	if err != nil {
		return "", err
	}
	if fmt.Sprintf("%x", sha256.Sum256(archive)) != expected {
		return "", fmt.Errorf("release checksum mismatch")
	}
	data, err := reportExecutable(archive, name, extension == ".zip")
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(target, 0700); err != nil {
		return "", err
	}
	staging, err := os.MkdirTemp(parent, ".download-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(staging)
	if err = os.WriteFile(filepath.Join(staging, name), data, 0755); err != nil {
		return "", err
	}
	if err = os.WriteFile(filepath.Join(staging, "binary.sha256"), []byte(fmt.Sprintf("%x", sha256.Sum256(data))), 0600); err != nil {
		return "", err
	}
	// Other processes wait on the lock while the two cache files are replaced.
	_ = os.Remove(binary)
	_ = os.Remove(filepath.Join(target, "binary.sha256"))
	if err = os.Rename(filepath.Join(staging, "binary.sha256"), filepath.Join(target, "binary.sha256")); err != nil {
		return "", err
	}
	if err = os.Rename(filepath.Join(staging, name), binary); err != nil {
		return "", err
	}
	return binary, nil
}

func readReportBytes(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err == nil && int64(len(data)) > limit {
		err = fmt.Errorf("release download too large")
	}
	return data, err
}

// Only the exact executable is read; archive paths never become filesystem paths.
func reportExecutable(archive []byte, name string, zipped bool) ([]byte, error) {
	var data []byte
	accept := func(r io.Reader) error {
		if data != nil {
			return fmt.Errorf("duplicate release executable")
		}
		var err error
		data, err = readReportBytes(r, maxReportBinary)
		return err
	}
	if zipped {
		z, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, err
		}
		for _, member := range z.File {
			if member.Name != name || !member.Mode().IsRegular() {
				continue
			}
			f, err := member.Open()
			if err != nil {
				return nil, err
			}
			err = accept(f)
			f.Close()
			if err != nil {
				return nil, err
			}
		}
	} else {
		gz, err := gzip.NewReader(bytes.NewReader(archive))
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		t := tar.NewReader(io.LimitReader(gz, maxReportBinary+8<<20))
		for {
			h, err := t.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			if h.Name == name && h.Typeflag == tar.TypeReg {
				if err = accept(t); err != nil {
					return nil, err
				}
			}
		}
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("release executable missing")
	}
	return data, nil
}
