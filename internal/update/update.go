// Package update handles checking for and applying dmed editor updates
// from GitHub Releases.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultRepo is the GitHub repository releases are fetched from.
	DefaultRepo = "dedomorozoff/dmed"

	// DefaultAPIEndpoint is the GitHub latest release endpoint.
	DefaultAPIEndpoint = "https://api.github.com/repos/" + DefaultRepo + "/releases/latest"

	// CacheDuration is how long a successful update check is cached.
	CacheDuration = 24 * time.Hour
)

// Release describes an available release on GitHub.
type Release struct {
	Tag         string    `json:"tag"`
	Version     string    `json:"version"`
	PublishedAt time.Time `json:"published_at"`
	HTMLURL     string    `json:"html_url"`
	AssetURL    string    `json:"asset_url"`
	AssetName   string    `json:"asset_name"`
	AssetSize   int64     `json:"asset_size"`
}

type githubRelease struct {
	TagName     string        `json:"tag_name"`
	HTMLURL     string        `json:"html_url"`
	PublishedAt time.Time     `json:"published_at"`
	Assets      []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type cacheData struct {
	CheckedAt time.Time `json:"checked_at"`
	Release   *Release  `json:"release,omitempty"`
}

// ExpectedAssetName returns the release binary name for the given OS and architecture.
func ExpectedAssetName(goos, goarch string) string {
	ext := ""
	if goos == "windows" {
		ext = ".exe"
	}
	return fmt.Sprintf("dmed-%s-%s%s", goos, goarch, ext)
}

// CleanVersion strips leading "v" or "V" and whitespace.
func CleanVersion(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "V")
	return v
}

// CompareSemver compares two semantic versions (e.g. "0.10.1" and "0.10.0").
// Returns 1 if a > b, -1 if a < b, and 0 if equal.
// "dev" is treated as older than any released version.
func CompareSemver(a, b string) int {
	a = CleanVersion(a)
	b = CleanVersion(b)

	if a == b {
		return 0
	}
	if a == "dev" || a == "" {
		return -1
	}
	if b == "dev" || b == "" {
		return 1
	}

	// Split pre-release tags like 0.10.1-rc1
	aMain, aPre, _ := strings.Cut(a, "-")
	bMain, bPre, _ := strings.Cut(b, "-")

	aParts := strings.Split(aMain, ".")
	bParts := strings.Split(bMain, ".")

	maxLen := len(aParts)
	if len(bParts) > maxLen {
		maxLen = len(bParts)
	}

	for i := 0; i < maxLen; i++ {
		var aNum, bNum int
		if i < len(aParts) {
			aNum, _ = strconv.Atoi(aParts[i])
		}
		if i < len(bParts) {
			bNum, _ = strconv.Atoi(bParts[i])
		}
		if aNum > bNum {
			return 1
		}
		if aNum < bNum {
			return -1
		}
	}

	// When main numbers match: a version without pre-release is newer than one with pre-release.
	if aPre == "" && bPre != "" {
		return 1
	}
	if aPre != "" && bPre == "" {
		return -1
	}
	return strings.Compare(aPre, bPre)
}

// CheckOptions configures an update check.
type CheckOptions struct {
	CurrentVersion string
	Force          bool
	CacheFile      string
	Endpoint       string
	HTTPClient     *http.Client
	GOOS           string
	GOARCH         string
}

// DefaultCacheFile returns the path to the update check cache file (~/.dmed/update_cache.json).
func DefaultCacheFile() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".dmed", "update_cache.json")
}

// Check checks GitHub Releases for a newer version of dmed.
// If force is false and the cache is recent (< 24 hours), it returns cached results.
// Returns nil, nil if already up to date.
func Check(opts CheckOptions) (*Release, error) {
	if opts.GOOS == "" {
		opts.GOOS = runtime.GOOS
	}
	if opts.GOARCH == "" {
		opts.GOARCH = runtime.GOARCH
	}
	if opts.CacheFile == "" {
		opts.CacheFile = DefaultCacheFile()
	}
	if opts.Endpoint == "" {
		if env := os.Getenv("DMED_UPDATE_URL"); env != "" {
			opts.Endpoint = env
		} else {
			opts.Endpoint = DefaultAPIEndpoint
		}
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}

	// 1. Check cache if not forced
	if !opts.Force && opts.CacheFile != "" {
		if cached, ok := readCache(opts.CacheFile); ok {
			if time.Since(cached.CheckedAt) < CacheDuration {
				if cached.Release != nil && CompareSemver(cached.Release.Version, opts.CurrentVersion) > 0 {
					return cached.Release, nil
				}
				return nil, nil
			}
		}
	}

	// 2. Fetch from GitHub API
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, opts.Endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create update request: %w", err)
	}
	req.Header.Set("User-Agent", fmt.Sprintf("dmed/%s (%s; %s)", opts.CurrentVersion, opts.GOOS, opts.GOARCH))
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := opts.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("check for updates: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned HTTP %d", resp.StatusCode)
	}

	var ghRel githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&ghRel); err != nil {
		return nil, fmt.Errorf("decode release json: %w", err)
	}

	relVersion := CleanVersion(ghRel.TagName)
	targetAsset := ExpectedAssetName(opts.GOOS, opts.GOARCH)

	var matchedAsset *githubAsset
	for i := range ghRel.Assets {
		if ghRel.Assets[i].Name == targetAsset {
			matchedAsset = &ghRel.Assets[i]
			break
		}
	}

	var foundRelease *Release
	if matchedAsset != nil {
		foundRelease = &Release{
			Tag:         ghRel.TagName,
			Version:     relVersion,
			PublishedAt: ghRel.PublishedAt,
			HTMLURL:     ghRel.HTMLURL,
			AssetURL:    matchedAsset.BrowserDownloadURL,
			AssetName:   matchedAsset.Name,
			AssetSize:   matchedAsset.Size,
		}
	}

	// 3. Write cache
	if opts.CacheFile != "" {
		_ = writeCache(opts.CacheFile, cacheData{
			CheckedAt: time.Now(),
			Release:   foundRelease,
		})
	}

	if foundRelease != nil && CompareSemver(foundRelease.Version, opts.CurrentVersion) > 0 {
		return foundRelease, nil
	}

	return nil, nil
}

func readCache(path string) (cacheData, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return cacheData{}, false
	}
	var cd cacheData
	if err := json.Unmarshal(data, &cd); err != nil {
		return cacheData{}, false
	}
	return cd, true
}

func writeCache(path string, cd cacheData) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.Marshal(cd)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// ProgressFunc reports download progress (bytes received, total expected bytes or -1).
type ProgressFunc func(received, total int64)

// ApplyOptions configures the update installation.
type ApplyOptions struct {
	Release    *Release
	Executable string
	Progress   ProgressFunc
	HTTPClient *http.Client
}

// Apply downloads and replaces the running executable with the new release binary.
func Apply(opts ApplyOptions) error {
	if opts.Release == nil || opts.Release.AssetURL == "" {
		return fmt.Errorf("no release asset URL provided")
	}

	targetExe := opts.Executable
	if targetExe == "" {
		var err error
		targetExe, err = os.Executable()
		if err != nil {
			return fmt.Errorf("locate executable: %w", err)
		}
	}

	// Resolve symlinks to target real binary file
	realExe, err := filepath.EvalSymlinks(targetExe)
	if err == nil {
		targetExe = realExe
	}

	targetDir := filepath.Dir(targetExe)

	// Check if directory is writable
	permCheck, err := os.CreateTemp(targetDir, ".dmed-perm-test-*")
	if err != nil {
		if os.IsPermission(err) {
			return fmt.Errorf("cannot write to %s (permission denied); if installed via package manager, update using your package manager (e.g. apt/pacman/brew)", targetDir)
		}
		return fmt.Errorf("cannot write to %s: %w", targetDir, err)
	}
	permCheckName := permCheck.Name()
	_ = permCheck.Close()
	_ = os.Remove(permCheckName)

	// Download new binary into a temp file in the same directory (ensuring atomic rename)
	ext := filepath.Ext(targetExe)
	tmpFile, err := os.CreateTemp(targetDir, "dmed-update-*"+ext)
	if err != nil {
		return fmt.Errorf("create temporary binary: %w", err)
	}
	tmpName := tmpFile.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()

	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}

	resp, err := client.Get(opts.Release.AssetURL)
	if err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("download %s: %w", opts.Release.AssetURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		_ = tmpFile.Close()
		return fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}

	total := resp.ContentLength
	var received int64

	writer := io.Writer(tmpFile)
	if opts.Progress != nil {
		writer = io.MultiWriter(tmpFile, &progressTracker{
			total:    total,
			received: &received,
			callback: opts.Progress,
		})
	}

	if _, err := io.Copy(writer, resp.Body); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("write binary: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temporary binary: %w", err)
	}

	// Set executable permissions on non-Windows
	if runtime.GOOS != "windows" {
		if err := os.Chmod(tmpName, 0755); err != nil {
			return fmt.Errorf("chmod binary: %w", err)
		}
	}

	// Replace executable
	if runtime.GOOS == "windows" {
		oldExe := targetExe + ".old"
		_ = os.Remove(oldExe)
		if err := os.Rename(targetExe, oldExe); err != nil {
			return fmt.Errorf("rename existing binary: %w", err)
		}
		if err := os.Rename(tmpName, targetExe); err != nil {
			// rollback
			_ = os.Rename(oldExe, targetExe)
			return fmt.Errorf("install new binary: %w", err)
		}
	} else {
		if err := os.Rename(tmpName, targetExe); err != nil {
			return fmt.Errorf("replace binary: %w", err)
		}
	}

	return nil
}

type progressTracker struct {
	total    int64
	received *int64
	callback ProgressFunc
}

func (pt *progressTracker) Write(p []byte) (int, error) {
	n := len(p)
	*pt.received += int64(n)
	if pt.callback != nil {
		pt.callback(*pt.received, pt.total)
	}
	return n, nil
}

// CleanupOld removes any leftover .old binary from a previous Windows update.
func CleanupOld() {
	if runtime.GOOS != "windows" {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	_ = os.Remove(exe + ".old")
}

// Restart relaunches the running dmed with the same arguments, preserving the
// working directory. It is called after Apply replaces the binary so the new
// code runs in a fresh process. On Windows the rename that Apply performed
// leaves the old process holding the .old file open, so the restart is the
// only way to free it; on other platforms the new binary starts and the old
// process exits on its own.
//
// The implementation lives in restart_unix.go and restart_windows.go because
// the process-group flag that detaches the new editor is not available on
// Windows, and the two differ in more than a build tag.
func Restart(args []string) error {
	return restart(args)
}
