package release

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func platformAssetName() string {
	return fmt.Sprintf("a2a-%s-%s", runtime.GOOS, runtime.GOARCH)
}

func TestDownload_FetchesAssetAndSums(t *testing.T) {
	t.Parallel()

	assetName := platformAssetName()
	assetBody := []byte("fake binary contents")
	sumsBody := []byte("deadbeef  " + assetName + "\n")
	bundleBody := []byte("fake cosign bundle")

	var privatePathHits, publicPathHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/asset/bin":
			privatePathHits++
			if r.Header.Get("Accept") != "application/octet-stream" {
				t.Errorf("asset request Accept = %q, want application/octet-stream", r.Header.Get("Accept"))
			}
			if r.Header.Get("Authorization") != "Bearer tok" {
				t.Errorf("asset request Authorization = %q, want Bearer tok", r.Header.Get("Authorization"))
			}
			_, _ = w.Write(assetBody)
		case "/dl/bin":
			publicPathHits++
			_, _ = w.Write(assetBody)
		case "/asset/sums", "/dl/sums":
			_, _ = w.Write(sumsBody)
		case "/asset/bundle", "/dl/bundle":
			_, _ = w.Write(bundleBody)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	rel := Release{
		Tag:     "v0.2.0",
		Version: "0.2.0",
		Assets: []Asset{
			{Name: assetName, URL: srv.URL + "/asset/bin", BrowserDownloadURL: srv.URL + "/dl/bin"},
			{Name: "SHA256SUMS", URL: srv.URL + "/asset/sums", BrowserDownloadURL: srv.URL + "/dl/sums"},
			{Name: assetName + ".cosign.bundle", URL: srv.URL + "/asset/bundle", BrowserDownloadURL: srv.URL + "/dl/bundle"},
		},
	}

	t.Run("private token path", func(t *testing.T) {
		dir := t.TempDir()
		result, err := Download(context.Background(), srv.Client(), "tok", rel, dir)
		if err != nil {
			t.Fatalf("Download: %v", err)
		}
		if privatePathHits == 0 {
			t.Fatal("expected private asset API path to be hit")
		}
		assertFileContent(t, result.AssetPath, assetBody)
		assertFileContent(t, result.SumsPath, sumsBody)
		if result.BundlePath == "" {
			t.Fatal("expected BundlePath to be populated when a bundle asset is present")
		}
		assertFileContent(t, result.BundlePath, bundleBody)
	})

	t.Run("public tokenless path", func(t *testing.T) {
		dir := t.TempDir()
		result, err := Download(context.Background(), srv.Client(), "", rel, dir)
		if err != nil {
			t.Fatalf("Download: %v", err)
		}
		if publicPathHits == 0 {
			t.Fatal("expected public browser_download_url path to be hit")
		}
		assertFileContent(t, result.AssetPath, assetBody)
	})
}

func TestDownload_NoBundle_BundlePathEmpty(t *testing.T) {
	t.Parallel()
	assetName := platformAssetName()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("body for " + r.URL.Path))
	}))
	defer srv.Close()

	rel := Release{
		Assets: []Asset{
			{Name: assetName, BrowserDownloadURL: srv.URL + "/bin"},
			{Name: "SHA256SUMS", BrowserDownloadURL: srv.URL + "/sums"},
		},
	}
	dir := t.TempDir()
	result, err := Download(context.Background(), srv.Client(), "", rel, dir)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if result.BundlePath != "" {
		t.Fatalf("BundlePath = %q, want empty (no bundle asset in release)", result.BundlePath)
	}
}

func TestDownload_PlatformAsset404_ErrorAndTempCleaned(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	assetName := platformAssetName()
	rel := Release{
		Assets: []Asset{
			{Name: assetName, BrowserDownloadURL: srv.URL + "/bin"},
			{Name: "SHA256SUMS", BrowserDownloadURL: srv.URL + "/sums"},
		},
	}
	dir := t.TempDir()
	_, err := Download(context.Background(), srv.Client(), "", rel, dir)
	if !errors.Is(err, ErrAssetNotFound) {
		t.Fatalf("Download() error = %v, want ErrAssetNotFound", err)
	}
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatalf("ReadDir: %v", readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("destDir not cleaned after 404: %v", entries)
	}
}

func TestDownload_PlatformAssetMissingFromRelease(t *testing.T) {
	t.Parallel()
	rel := Release{Assets: []Asset{{Name: "SHA256SUMS", BrowserDownloadURL: "http://unused"}}}
	dir := t.TempDir()
	_, err := Download(context.Background(), http.DefaultClient, "", rel, dir)
	if !errors.Is(err, ErrAssetNotFound) {
		t.Fatalf("Download() error = %v, want ErrAssetNotFound", err)
	}
}

func assertFileContent(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	if string(got) != string(want) {
		t.Fatalf("file %s content = %q, want %q", filepath.Base(path), got, want)
	}
}
