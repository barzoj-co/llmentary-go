// SPDX-License-Identifier: MIT

package llmentary

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testZip(t *testing.T, name string, payload []byte) (archive []byte, sum string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	archive = buf.Bytes()
	digest := sha256.Sum256(archive)
	return archive, hex.EncodeToString(digest[:])
}

func TestDownloadReleaseWindowsZip(t *testing.T) {
	archive, sum := testZip(t, "llmentary.exe", []byte("windows-proxy"))
	archiveName := "llmentary_0.1.0_windows_amd64.zip"
	mux := http.NewServeMux()
	mux.HandleFunc("/v0.1.0/"+archiveName, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	})
	mux.HandleFunc("/v0.1.0/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(sum + "  " + archiveName + "\n"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	path, err := DownloadRelease(context.Background(), DownloadOptions{
		Version:    "v0.1.0",
		DestDir:    t.TempDir(),
		BaseURL:    srv.URL,
		GOOS:       "windows",
		GOARCH:     "amd64",
		HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != BinaryName+".exe" {
		t.Fatalf("extracted name=%q", path)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "windows-proxy" {
		t.Fatalf("contents=%q", got)
	}
}

func TestDownloadReleaseAPIMissingAssets(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := []byte(`{"tag_name":"v0.1.0","assets":[{"name":"notes.txt","url":"https://api.github.com/unused"}]}`)
		if !strings.Contains(req.URL.Path, "/releases/tags/") {
			body = []byte("nope")
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(body)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}
	_, err := DownloadRelease(context.Background(), DownloadOptions{
		Version:    "v0.1.0",
		DestDir:    t.TempDir(),
		Token:      "tok",
		GOOS:       "linux",
		GOARCH:     "amd64",
		HTTPClient: client,
	})
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing assets err=%v", err)
	}
}

func TestExtractZipSkipsTraversalAndMissingBinary(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("../llmentary")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, "nope"); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := extractZipBinary(buf.Bytes(), map[string]bool{BinaryName: true}); err == nil {
		t.Fatal("traversal-only zip should not extract")
	}

	if _, err := extractZipBinary([]byte("not-a-zip"), map[string]bool{BinaryName: true}); err == nil {
		t.Fatal("invalid zip")
	}

	empty, _ := testZip(t, "readme.txt", []byte("docs"))
	if _, err := extractZipBinary(empty, map[string]bool{BinaryName: true}); err == nil || !strings.Contains(err.Error(), "did not contain") {
		t.Fatalf("missing binary err=%v", err)
	}
}

func TestReleaseAssetURLAndRedactedMalformed(t *testing.T) {
	got := releaseAssetURL("", "barzoj-co/llmentary", "v0.4.0", "checksums.txt")
	if got != "https://github.com/barzoj-co/llmentary/releases/download/v0.4.0/checksums.txt" {
		t.Fatalf("public asset URL=%q", got)
	}
	leaked := redactedURL("not a url?token=secret")
	if strings.Contains(leaked, "token=secret") {
		t.Fatalf("query should be stripped: %q", leaked)
	}
	leaked = redactedURL("https://user:pw@bad")
	if strings.Contains(leaked, "pw") || strings.Contains(leaked, "user") {
		t.Fatalf("userinfo should be stripped: %q", leaked)
	}
}

func TestExtractTarGzSkipsDirAndTraversal(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "subdir/", Mode: 0755, Typeflag: tar.TypeDir}); err != nil {
		t.Fatal(err)
	}
	if err := tw.WriteHeader(&tar.Header{Name: "../llmentary", Mode: 0755, Size: 4, Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("nope")); err != nil {
		t.Fatal(err)
	}
	payload := []byte("real-bin")
	if err := tw.WriteHeader(&tar.Header{Name: "llmentary", Mode: 0755, Size: int64(len(payload)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := extractTarGzBinary(buf.Bytes(), map[string]bool{BinaryName: true})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "real-bin" {
		t.Fatalf("extracted=%q", got)
	}
	if _, err := extractTarGzBinary([]byte("not-gzip"), map[string]bool{BinaryName: true}); err == nil {
		t.Fatal("invalid gzip")
	}
}

func TestDownloadReleasePublicGitHubURLs(t *testing.T) {
	t.Setenv("LLMENTARY_GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("LLMTESTLAB_REPO_TOKEN", "")

	archive, sum := testTarGz(t, "llmentary", []byte("public-proxy"))
	archiveName := "llmentary_0.1.0_linux_amd64.tar.gz"
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := []byte("not found")
		status := http.StatusNotFound
		switch {
		case strings.HasSuffix(req.URL.Path, "/checksums.txt"):
			body = []byte(sum + "  " + archiveName + "\n")
			status = http.StatusOK
		case strings.HasSuffix(req.URL.Path, "/"+archiveName):
			body = archive
			status = http.StatusOK
		}
		if !strings.Contains(req.URL.Host, "github.com") {
			t.Errorf("expected github.com host, got %s", req.URL.Host)
		}
		return &http.Response{
			StatusCode: status,
			Body:       io.NopCloser(bytes.NewReader(body)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}
	path, err := DownloadRelease(context.Background(), DownloadOptions{
		Version:    "v0.1.0",
		DestDir:    t.TempDir(),
		GOOS:       "linux",
		GOARCH:     "amd64",
		HTTPClient: client,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte("public-proxy")) {
		t.Fatalf("contents=%q", got)
	}
}

func TestDownloadReleaseMissingArchive(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v0.1.0/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  llmentary_0.1.0_linux_amd64.tar.gz\n"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	_, err := DownloadRelease(context.Background(), DownloadOptions{
		Version:    "v0.1.0",
		DestDir:    t.TempDir(),
		BaseURL:    srv.URL,
		GOOS:       "linux",
		GOARCH:     "amd64",
		HTTPClient: srv.Client(),
	})
	if err == nil || !strings.Contains(err.Error(), "download archive") {
		t.Fatalf("missing archive err=%v", err)
	}
}

func TestDownloadReleaseRequiresFieldsAndFirstNonEmpty(t *testing.T) {
	if _, err := DownloadRelease(context.Background(), DownloadOptions{DestDir: t.TempDir()}); err == nil {
		t.Fatal("missing Version")
	}
	if _, err := DownloadRelease(context.Background(), DownloadOptions{Version: "v0.1.0"}); err == nil {
		t.Fatal("missing DestDir")
	}
	if got := firstNonEmpty("", " ", "keep"); got != "keep" {
		t.Fatalf("firstNonEmpty=%q", got)
	}
	if got := firstNonEmpty("", ""); got != "" {
		t.Fatalf("firstNonEmpty empty=%q", got)
	}
}

func TestProxyConfigObjectUpstreamsAndAbortPhaseFromRaw(t *testing.T) {
	var cfg ProxyConfig
	if err := json.Unmarshal([]byte(`{
		"version":"obj",
		"upstreams":{"openai":{"url":"https://api.openai.com/v1"},"alt":{"base_url":"https://alt.example/v1"}}
	}`), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Upstreams["openai"] != "https://api.openai.com/v1" || cfg.Upstreams["alt"] != "https://alt.example/v1" {
		t.Fatalf("object upstreams=%v", cfg.Upstreams)
	}

	var call Call
	if err := json.Unmarshal([]byte(`{
		"id":"c",
		"client_aborted":true,
		"normalized":{"client_aborted":true,"raw":{"phase":"after_headers"}}
	}`), &call); err != nil {
		t.Fatal(err)
	}
	if call.AbortPhase != AbortPhaseAfterHeaders {
		t.Fatalf("phase from raw=%q", call.AbortPhase)
	}

	call = Call{}
	if err := json.Unmarshal([]byte(`{
		"id":"c2",
		"client_aborted":true,
		"normalized":{"client_aborted":true,"raw":{"abort_phase":"before_headers"}}
	}`), &call); err != nil {
		t.Fatal(err)
	}
	if call.AbortPhase != AbortPhaseBeforeHeaders {
		t.Fatalf("abort_phase from raw=%q", call.AbortPhase)
	}
}

func TestSmallUncoveredHelpers(t *testing.T) {
	if _, err := checksumForFile([]byte("abcd  llmentary.tar.gz\n"), "llmentary.tar.gz"); err == nil || !strings.Contains(err.Error(), "checksum length") {
		t.Fatalf("short checksum err=%v", err)
	}
	if _, err := checksumForFile([]byte("# comment\nonlyone\n"), "llmentary.tar.gz"); err == nil {
		t.Fatal("missing checksum entry")
	}
	var call Call
	if err := json.Unmarshal([]byte(`{`), &call); err == nil {
		t.Fatal("invalid call JSON")
	}
	var cfg ProxyConfig
	if err := json.Unmarshal([]byte(`{`), &cfg); err == nil {
		t.Fatal("invalid config JSON")
	}
	if err := json.Unmarshal([]byte(`{"version":1}`), &cfg); err == nil {
		t.Fatal("non-string version")
	}
	if err := json.Unmarshal([]byte(`{"upstreams":true}`), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Upstreams != nil {
		t.Fatalf("true upstreams=%v", cfg.Upstreams)
	}
	if abortPhaseFromNormalized(Normalized{Raw: json.RawMessage(`{`)}) != "" {
		t.Fatal("invalid normalized raw")
	}
	if abortPhaseFromNormalized(Normalized{Raw: json.RawMessage(`{"phase":"nope"}`)}) != "" {
		t.Fatal("unknown phase")
	}
	if got := formatWaitDuration(-time.Second); got != "0s" {
		t.Fatalf("formatWaitDuration(-1s)=%q", got)
	}
}
