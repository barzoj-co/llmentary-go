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
	"path/filepath"
	"strings"
	"testing"
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

	var gotURLs []string
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotURLs = append(gotURLs, req.URL.String())
		if auth := req.Header.Get("Authorization"); auth != "Bearer from-llmentary" {
			t.Errorf("Authorization=%q", auth)
		}
		body := []byte("not found")
		status := http.StatusNotFound
		switch {
		case strings.HasSuffix(req.URL.Path, "/checksums.txt"):
			body = []byte(sum + "  " + wantArchive + "\n" + sum + "  " + oldArchive + "\n")
			status = http.StatusOK
		case strings.HasSuffix(req.URL.Path, "/"+wantArchive):
			body = archive
			status = http.StatusOK
		case strings.Contains(req.URL.Path, oldArchive):
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
		t.Fatal("expected GitHub download URLs")
	}
	for _, raw := range gotURLs {
		if strings.Contains(raw, "llm-test-lab") {
			t.Fatalf("old name in download URL %s", raw)
		}
		if !strings.Contains(raw, "https://github.com/barzoj-co/llmentary/releases/download/v0.1.0/") {
			t.Fatalf("expected default repo URL, got %s", raw)
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
