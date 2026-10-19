// SPDX-License-Identifier: MIT

package llmentary

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestDefaultReleaseRepo(t *testing.T) {
	if defaultReleaseRepo != "barzoj-co/llmentary" {
		t.Fatalf("defaultReleaseRepo=%q", defaultReleaseRepo)
	}
}

func TestProxyVersion(t *testing.T) {
	if ProxyVersion != "v0.1.0" {
		t.Fatalf("ProxyVersion=%q want v0.1.0", ProxyVersion)
	}
	tag, version := normalizeReleaseVersion(ProxyVersion)
	if tag != "v0.1.0" || version != "0.1.0" {
		t.Fatalf("normalizeReleaseVersion(%q)=(%q, %q)", ProxyVersion, tag, version)
	}
	got := releaseArchiveName(version, "linux", "amd64")
	if got != "llmentary_0.1.0_linux_amd64.tar.gz" {
		t.Fatalf("releaseArchiveName=%q", got)
	}
}

func TestDownloadPinnedProxyRelease(t *testing.T) {
	if os.Getenv("LLMENTARY_LIVE_DOWNLOAD") != "1" {
		t.Skip("set LLMENTARY_LIVE_DOWNLOAD=1 to download barzoj-co/llmentary@" + ProxyVersion)
	}
	dest := os.Getenv("LLMENTARY_LIVE_DOWNLOAD_DIR")
	if dest == "" {
		dest = t.TempDir()
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	path, err := DownloadRelease(ctx, DownloadOptions{
		Version: ProxyVersion,
		DestDir: dest,
	})
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.IsDir() || info.Size() < 1024 {
		t.Fatalf("extracted binary looks empty: path=%s size=%d", path, info.Size())
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("extracted binary is not executable: mode=%s", info.Mode())
	}
	cmd := exec.Command(path, "--help")
	out, err := cmd.CombinedOutput()
	t.Logf("llmentary --help: err=%v\n%s", err, out)
	if err != nil && len(out) == 0 {
		t.Fatalf("running downloaded binary: %v", err)
	}
}

func TestReleaseArchiveName(t *testing.T) {
	got := releaseArchiveName("0.1.0", "linux", "amd64")
	want := "llmentary_0.1.0_linux_amd64.tar.gz"
	if got != want {
		t.Fatalf("releaseArchiveName=%q want %q", got, want)
	}
	old := "llm-test-lab_0.1.0_linux_amd64.tar.gz"
	if got == old {
		t.Fatal("asset matcher still uses llm-test-lab archive names")
	}
	sum := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if _, err := checksumForFile([]byte(sum+"  "+old+"\n"), got); err == nil {
		t.Fatal("expected checksum lookup to reject llm-test-lab_0.1.0_linux_amd64.tar.gz")
	}
}

func TestDownloadReleaseUsesLlmentaryGithubToken(t *testing.T) {
	t.Setenv("LLMENTARY_GITHUB_TOKEN", "from-llmentary")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("LLMTESTLAB_REPO_TOKEN", "should-not-win")

	archive, sum := testTarGz(t, "llmentary", []byte("payload"))
	wantArchive := "llmentary_0.1.0_linux_amd64.tar.gz"
	oldArchive := "llm-test-lab_0.1.0_linux_amd64.tar.gz"
	meta := []byte(`{"tag_name":"v0.1.0","assets":[` +
		`{"name":"checksums.txt","url":"https://api.github.com/repos/barzoj-co/llmentary/releases/assets/1"},` +
		`{"name":"` + wantArchive + `","url":"https://api.github.com/repos/barzoj-co/llmentary/releases/assets/2"},` +
		`{"name":"` + oldArchive + `","url":"https://api.github.com/repos/barzoj-co/llmentary/releases/assets/3"}` +
		`]}`)

	var gotURLs []string
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotURLs = append(gotURLs, req.URL.String())
		if auth := req.Header.Get("Authorization"); auth != "Bearer from-llmentary" {
			t.Errorf("Authorization=%q", auth)
		}
		if ua := req.Header.Get("User-Agent"); ua != "llmentary-go" {
			t.Errorf("User-Agent=%q", ua)
		}
		body := []byte("not found")
		status := http.StatusNotFound
		switch {
		case req.URL.Path == "/repos/barzoj-co/llmentary/releases/tags/v0.1.0":
			if req.Header.Get("Accept") != "application/vnd.github+json" {
				t.Errorf("Accept=%q", req.Header.Get("Accept"))
			}
			body = meta
			status = http.StatusOK
		case strings.HasSuffix(req.URL.Path, "/releases/assets/1"):
			body = []byte(sum + "  " + wantArchive + "\n" + sum + "  " + oldArchive + "\n")
			status = http.StatusOK
		case strings.HasSuffix(req.URL.Path, "/releases/assets/2"):
			body = archive
			status = http.StatusOK
		case strings.Contains(req.URL.Path, oldArchive) || strings.HasSuffix(req.URL.Path, "/releases/assets/3"):
			t.Errorf("fetched old archive name %s", req.URL)
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
	if filepath.Base(path) != BinaryName {
		t.Fatalf("extracted name=%q", path)
	}
	if len(gotURLs) == 0 {
		t.Fatal("expected GitHub API URLs")
	}
	for _, raw := range gotURLs {
		if strings.Contains(raw, "llm-test-lab") {
			t.Fatalf("old name in download URL %s", raw)
		}
		if !strings.Contains(raw, "https://api.github.com/repos/barzoj-co/llmentary/") {
			t.Fatalf("expected GitHub API URL, got %s", raw)
		}
		if strings.Contains(raw, "/releases/download/") {
			t.Fatalf("private download should not use browser URL %s", raw)
		}
	}
}

func TestDownloadReleaseVerifiesChecksum(t *testing.T) {
	archive, sum := testTarGz(t, "llmentary", []byte("#!/bin/sh\necho proxy\n"))
	archiveName := "llmentary_0.1.0_linux_amd64.tar.gz"
	mux := http.NewServeMux()
	mux.HandleFunc("/v0.1.0/"+archiveName, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	})
	mux.HandleFunc("/v0.1.0/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(sum + "  " + archiveName + "\n"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	dest := t.TempDir()
	path, err := DownloadRelease(context.Background(), DownloadOptions{
		Version:    "v0.1.0",
		DestDir:    dest,
		BaseURL:    srv.URL,
		GOOS:       "linux",
		GOARCH:     "amd64",
		HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte("echo proxy")) {
		t.Fatalf("extracted binary contents=%q", got)
	}
	if filepath.Base(path) != BinaryName {
		t.Fatalf("extracted name=%q", path)
	}
}

func TestDownloadReleaseChecksumMismatch(t *testing.T) {
	archive, _ := testTarGz(t, "llmentary", []byte("payload"))
	archiveName := "llmentary_0.1.0_linux_amd64.tar.gz"
	mux := http.NewServeMux()
	mux.HandleFunc("/v0.1.0/"+archiveName, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	})
	mux.HandleFunc("/v0.1.0/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  " + archiveName + "\n"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	_, err := DownloadRelease(context.Background(), DownloadOptions{
		Version:    "0.1.0",
		DestDir:    t.TempDir(),
		BaseURL:    srv.URL,
		GOOS:       "linux",
		GOARCH:     "amd64",
		HTTPClient: srv.Client(),
	})
	if err == nil {
		t.Fatal("expected checksum mismatch")
	}
}

func TestDownloadReleaseDoesNotSendAmbientTokenToCustomHost(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ambient-secret")
	t.Setenv("GH_TOKEN", "ambient-secret")
	t.Setenv("LLMENTARY_GITHUB_TOKEN", "ambient-secret")
	t.Setenv("LLMTESTLAB_REPO_TOKEN", "ambient-secret")

	archive, sum := testTarGz(t, "llmentary", []byte("payload"))
	archiveName := "llmentary_0.1.0_linux_amd64.tar.gz"
	mux := http.NewServeMux()
	mux.HandleFunc("/v0.1.0/"+archiveName, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("unexpected Authorization header %q", got)
		}
		_, _ = w.Write(archive)
	})
	mux.HandleFunc("/v0.1.0/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("unexpected Authorization header %q", got)
		}
		_, _ = w.Write([]byte(sum + "  " + archiveName + "\n"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	if _, err := DownloadRelease(context.Background(), DownloadOptions{
		Version:    "v0.1.0",
		DestDir:    t.TempDir(),
		BaseURL:    srv.URL,
		GOOS:       "linux",
		GOARCH:     "amd64",
		HTTPClient: srv.Client(),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRedactedURLStripsUserinfo(t *testing.T) {
	got := redactedURL("https://user:password@example.test/path?token=abc")
	if strings.Contains(got, "password") || strings.Contains(got, "token=") || strings.Contains(got, "user") {
		t.Fatalf("redactedURL leaked secrets: %q", got)
	}
	if !strings.Contains(got, "example.test/path") {
		t.Fatalf("redactedURL=%q", got)
	}
}

func TestChecksumForFile(t *testing.T) {
	sum := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	got, err := checksumForFile([]byte(sum+"  llmentary_0.1.0_linux_amd64.tar.gz\n"), "llmentary_0.1.0_linux_amd64.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if got != sum {
		t.Fatalf("got %s", got)
	}
}

func testTarGz(t *testing.T, name string, payload []byte) (archive []byte, sum string) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	hdr := &tar.Header{Name: name, Mode: 0755, Size: int64(len(payload))}
	if err := tw.WriteHeader(hdr); err != nil {
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
	archive = buf.Bytes()
	digest := sha256.Sum256(archive)
	return archive, hex.EncodeToString(digest[:])
}
