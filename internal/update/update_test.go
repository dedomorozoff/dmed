package update

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestCompareSemver(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"0.10.1", "0.10.0", 1},
		{"0.10.0", "0.10.1", -1},
		{"0.10.1", "0.10.1", 0},
		{"v0.10.1", "0.10.1", 0},
		{"v0.10.1", "v0.10.0", 1},
		{"0.9.7", "0.10.0", -1},
		{"1.0.0", "0.10.1", 1},
		{"0.10.1", "dev", 1},
		{"dev", "0.10.1", -1},
		{"dev", "dev", 0},
		{"0.10.1", "0.10.1-rc1", 1},
		{"0.10.1-rc1", "0.10.1", -1},
		{"0.10.1-rc1", "0.10.1-rc2", -1},
	}

	for _, tc := range tests {
		got := CompareSemver(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("CompareSemver(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestExpectedAssetName(t *testing.T) {
	if got := ExpectedAssetName("linux", "amd64"); got != "dmed-linux-amd64" {
		t.Errorf("got %q, want dmed-linux-amd64", got)
	}
	if got := ExpectedAssetName("windows", "amd64"); got != "dmed-windows-amd64.exe" {
		t.Errorf("got %q, want dmed-windows-amd64.exe", got)
	}
	if got := ExpectedAssetName("darwin", "arm64"); got != "dmed-darwin-arm64" {
		t.Errorf("got %q, want dmed-darwin-arm64", got)
	}
}

func TestCheckUpdate(t *testing.T) {
	targetAsset := ExpectedAssetName(runtime.GOOS, runtime.GOARCH)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ghRel := githubRelease{
			TagName:     "v0.10.2",
			HTMLURL:     "https://github.com/dedomorozoff/dmed/releases/tag/v0.10.2",
			PublishedAt: time.Now(),
			Assets: []githubAsset{
				{
					Name:               targetAsset,
					Size:               1234567,
					BrowserDownloadURL: "http://example.com/" + targetAsset,
				},
				{
					Name:               "other-asset",
					Size:               100,
					BrowserDownloadURL: "http://example.com/other",
				},
			},
		}
		_ = json.NewEncoder(w).Encode(ghRel)
	}))
	defer srv.Close()

	cacheDir := t.TempDir()
	cacheFile := filepath.Join(cacheDir, "cache.json")

	// 1. Check with older version -> update found
	rel, err := Check(CheckOptions{
		CurrentVersion: "0.10.1",
		Force:          true,
		CacheFile:      cacheFile,
		Endpoint:       srv.URL,
		HTTPClient:     srv.Client(),
	})
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}
	if rel == nil {
		t.Fatal("expected update to be found, got nil")
	}
	if rel.Version != "0.10.2" {
		t.Errorf("expected version 0.10.2, got %q", rel.Version)
	}
	if rel.AssetName != targetAsset {
		t.Errorf("expected asset name %q, got %q", targetAsset, rel.AssetName)
	}

	// 2. Check using cache (without force)
	relCached, err := Check(CheckOptions{
		CurrentVersion: "0.10.1",
		Force:          false,
		CacheFile:      cacheFile,
		Endpoint:       "http://invalid.url.that.would.fail",
		HTTPClient:     srv.Client(),
	})
	if err != nil {
		t.Fatalf("Cached check failed: %v", err)
	}
	if relCached == nil || relCached.Version != "0.10.2" {
		t.Errorf("expected cached release 0.10.2, got %+v", relCached)
	}

	// 3. Check with current version == latest -> nil
	relUpToDate, err := Check(CheckOptions{
		CurrentVersion: "0.10.2",
		Force:          true,
		CacheFile:      cacheFile,
		Endpoint:       srv.URL,
		HTTPClient:     srv.Client(),
	})
	if err != nil {
		t.Fatalf("Up-to-date check failed: %v", err)
	}
	if relUpToDate != nil {
		t.Errorf("expected nil release for up-to-date, got %+v", relUpToDate)
	}
}

func TestApplyUpdate(t *testing.T) {
	newBinaryContent := []byte("#!/bin/sh\necho updated\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(newBinaryContent)))
		_, _ = w.Write(newBinaryContent)
	}))
	defer srv.Close()

	dir := t.TempDir()
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	fakeExe := filepath.Join(dir, "dmed"+ext)
	if err := os.WriteFile(fakeExe, []byte("original binary"), 0755); err != nil {
		t.Fatal(err)
	}

	var progressCalled bool
	var progressReceived int64

	rel := &Release{
		Version:   "0.10.2",
		AssetURL:  srv.URL + "/download",
		AssetName: "dmed" + ext,
		AssetSize: int64(len(newBinaryContent)),
	}

	err := Apply(ApplyOptions{
		Release:    rel,
		Executable: fakeExe,
		HTTPClient: srv.Client(),
		Progress: func(received, total int64) {
			progressCalled = true
			progressReceived = received
		},
	})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	if !progressCalled {
		t.Error("progress callback was never called")
	}
	if progressReceived != int64(len(newBinaryContent)) {
		t.Errorf("progress received %d, want %d", progressReceived, len(newBinaryContent))
	}

	updatedContent, err := os.ReadFile(fakeExe)
	if err != nil {
		t.Fatal(err)
	}
	if string(updatedContent) != string(newBinaryContent) {
		t.Errorf("content not updated: got %q, want %q", updatedContent, newBinaryContent)
	}

	// On Windows, fakeExe.old should exist after update
	if runtime.GOOS == "windows" {
		oldContent, err := os.ReadFile(fakeExe + ".old")
		if err != nil {
			t.Errorf("expected %s.old to exist: %v", fakeExe, err)
		} else if string(oldContent) != "original binary" {
			t.Errorf("expected .old content 'original binary', got %q", oldContent)
		}
	}
}

func TestCleanupOld(t *testing.T) {
	// Call CleanupOld to ensure it runs without panicking
	CleanupOld()
}
