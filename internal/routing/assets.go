package routing

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxAssetSize = 32 << 20

func validateAssetURL(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return errors.New("must be an HTTPS URL without credentials")
	}
	return nil
}

// EnsureAssets stores a profile's geo files separately from the standard Xray
// files. This lets a Happ profile use its own category names without changing
// the files used by other profiles or by temporary server probes.
func EnsureAssets(ctx context.Context, profile Profile, root, standard string) (string, error) {
	if profile.GeoIPURL == "" && profile.GeoSiteURL == "" {
		return "", nil
	}
	if err := profile.Validate(); err != nil {
		return "", err
	}
	if profile.ID == "" || strings.ContainsAny(profile.ID, `/\.`) {
		return "", errors.New("invalid routing profile ID")
	}
	dir := filepath.Join(root, profile.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create routing asset directory: %w", err)
	}
	client := &http.Client{
		Timeout: 60 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 || req.URL.Scheme != "https" {
				return errors.New("geo asset redirect is not allowed")
			}
			return nil
		},
	}
	for _, asset := range []struct{ name, url string }{{"geoip.dat", profile.GeoIPURL}, {"geosite.dat", profile.GeoSiteURL}} {
		path := filepath.Join(dir, asset.name)
		if info, err := os.Stat(path); err == nil && info.Size() > 0 {
			continue
		}
		if asset.url == "" {
			source := filepath.Join(standard, asset.name)
			if info, err := os.Stat(source); err != nil || info.Size() == 0 {
				return "", fmt.Errorf("standard %s is unavailable", asset.name)
			}
			_ = os.Remove(path)
			if err := os.Symlink(source, path); err != nil {
				return "", fmt.Errorf("link standard %s: %w", asset.name, err)
			}
			continue
		}
		if err := downloadAsset(ctx, client, asset.url, path); err != nil {
			return "", fmt.Errorf("download %s: %w", asset.name, err)
		}
	}
	return dir, nil
}

func downloadAsset(ctx context.Context, client *http.Client, rawURL, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxAssetSize {
		return errors.New("asset exceeds 32 MiB")
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".asset-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	n, err := io.Copy(temp, io.LimitReader(resp.Body, maxAssetSize+1))
	if err != nil || n == 0 || n > maxAssetSize {
		temp.Close()
		if err != nil {
			return err
		}
		return errors.New("asset is empty or exceeds 32 MiB")
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}
