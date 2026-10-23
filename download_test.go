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
	if ProxyVersion != "v0.5.1" {
		t.Fatalf("ProxyVersion=%q want v0.5.1", ProxyVersion)
	}
	tag, version := normalizeReleaseVersion(ProxyVersion)
	if tag != "v0.5.1" || version != "0.5.1" {
		t.Fatalf("normalizeReleaseVersion(%q)=(%q, %q)", ProxyVersion, tag, version)
	}
	got := releaseArchiveName(version, "linux", "amd64")
	if got != "llmentary_0.5.1_linux_amd64.tar.gz" {
		t.Fatalf("releaseArchiveName=%q", got)
	}
}

func TestDownloadPinnedProxyRelease(t *testing.T) {
	if os.Getenv("LLMENTARY_LIVE_DOWNLOAD") != "1" {
		t.Skip("set LLMENTARY_LIVE_DOWNLOAD=1 to download barzoj-co/llmentary@" + ProxyVersion)
	}
	ctxSums, cancelSums := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelSums()
	sumsURL := releaseAssetURL("", defaultReleaseRepo, ProxyVersion, checksumsName)
	liveSums, err := httpGetBytesAccept(ctxSums, http.DefaultClient, sumsURL, optionalGitHubToken(), 1<<20, "")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(liveSums, publishedPinnedChecksums(t)) {
		t.Fatalf("live %s checksums.txt does not match testdata/release/checksums.txt", ProxyVersion)
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
	verCtx, verCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer verCancel()
	cmd := exec.CommandContext(verCtx, path, "--version")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	out, err := cmd.CombinedOutput()
	gotVer := strings.TrimSpace(string(out))
	if err != nil {
		t.Fatalf("running downloaded binary: %v\n%s", err, out)
	}
	t.Logf("llmentary --version=%s", gotVer)
	if !strings.Contains(gotVer, "llmentary 0.5.1") || !strings.Contains(gotVer, "go1.26.10") {
		t.Fatalf("llmentary --version=%q want llmentary 0.5.1 (..., go1.26.10)", gotVer)
	}
}

func TestReleaseArchiveName(t *testing.T) {
	_, version := normalizeReleaseVersion(ProxyVersion)
	got := releaseArchiveName(version, "linux", "amd64")
	want := "llmentary_" + version + "_linux_amd64.tar.gz"
	if got != want {
		t.Fatalf("releaseArchiveName=%q want %q", got, want)
	}
	if got == "llmentary_0.5.0_linux_amd64.tar.gz" {
		t.Fatal("archive name still uses previous pin v0.5.0")
	}
	old := "llm-test-lab_" + version + "_linux_amd64.tar.gz"
	if got == old {
		t.Fatal("asset matcher still uses llm-test-lab archive names")
	}
	sum := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if _, err := checksumForFile([]byte(sum+"  "+old+"\n"), got); err == nil {
		t.Fatal("expected checksum lookup to reject llm-test-lab archive names")
	}
	stale := "llmentary_0.5.0_linux_amd64.tar.gz"
	if _, err := checksumForFile([]byte(sum+"  "+stale+"\n"), got); err == nil {
		t.Fatal("expected checksum lookup to reject previous pin v0.5.0 assets")
	}
}

func TestDownloadReleaseUsesLlmentaryGithubToken(t *testing.T) {
	t.Setenv("LLMENTARY_GITHUB_TOKEN", "from-llmentary")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("LLMTESTLAB_REPO_TOKEN", "should-not-win")
	if got := optionalGitHubToken(); got != "from-llmentary" {
		t.Fatalf("optionalGitHubToken=%q", got)
	}

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
			t.Fatalf("tokened download should not use browser URL %s", raw)
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
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("checksum mismatch err=%v", err)
	}
}

func TestDownloadReleaseMissingChecksums(t *testing.T) {
	archive, _ := testTarGz(t, "llmentary", []byte("payload"))
	archiveName := "llmentary_0.1.0_linux_amd64.tar.gz"
	mux := http.NewServeMux()
	mux.HandleFunc("/v0.1.0/"+archiveName, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
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
	if err == nil || !strings.Contains(err.Error(), "download checksums") {
		t.Fatalf("missing checksums err=%v", err)
	}
}

func TestDownloadReleaseRejectsStalePinChecksums(t *testing.T) {
	archive, sum := testTarGz(t, "llmentary", []byte("payload"))
	_, version := normalizeReleaseVersion(ProxyVersion)
	archiveName := releaseArchiveName(version, "linux", "amd64")
	staleName := "llmentary_0.5.0_linux_amd64.tar.gz"
	mux := http.NewServeMux()
	mux.HandleFunc("/"+ProxyVersion+"/"+archiveName, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	})
	mux.HandleFunc("/"+ProxyVersion+"/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(sum + "  " + staleName + "\n"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	_, err := DownloadRelease(context.Background(), DownloadOptions{
		Version:    ProxyVersion,
		DestDir:    t.TempDir(),
		BaseURL:    srv.URL,
		GOOS:       "linux",
		GOARCH:     "amd64",
		HTTPClient: srv.Client(),
	})
	if err == nil || !strings.Contains(err.Error(), "no checksum entry") {
		t.Fatalf("stale pin checksums err=%v", err)
	}
}

func TestPinnedProxyReleaseAssetNames(t *testing.T) {
	if ProxyVersion != "v0.5.1" {
		t.Fatalf("ProxyVersion=%q want v0.5.1", ProxyVersion)
	}
	_, version := normalizeReleaseVersion(ProxyVersion)
	sums := publishedPinnedChecksums(t)
	for _, p := range pinnedReleasePlatforms() {
		name := releaseArchiveName(version, p.goos, p.goarch)
		if !strings.HasSuffix(name, "_"+p.goos+"_"+p.goarch+p.ext) {
			t.Fatalf("releaseArchiveName(%s/%s)=%q", p.goos, p.goarch, name)
		}
		if strings.Contains(name, "0.5.0") {
			t.Fatalf("platform asset still names previous pin: %s", name)
		}
		got, err := checksumForFile(sums, name)
		if err != nil {
			t.Fatalf("checksum for %s: %v", name, err)
		}
		if len(got) != 64 {
			t.Fatalf("checksum for %s has length %d", name, len(got))
		}
	}
	missing := releaseArchiveName(version, "linux", "amd64")
	if _, err := checksumForFile([]byte("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef  llmentary_0.5.0_linux_amd64.tar.gz\n"), missing); err == nil {
		t.Fatal("checksums for previous pin must not satisfy current linux/amd64 asset")
	}
}

func TestPinnedReleaseChecksumsMatchPublishedAssets(t *testing.T) {
	sums := publishedPinnedChecksums(t)
	_, version := normalizeReleaseVersion(ProxyVersion)
	if !bytes.Contains(sums, []byte("llmentary_"+version+"_")) {
		t.Fatalf("testdata checksums do not name pin %s", version)
	}
	if bytes.Contains(sums, []byte("llmentary_0.5.0_")) {
		t.Fatal("testdata checksums still list previous pin v0.5.0")
	}
	seen := map[string]string{}
	for _, p := range pinnedReleasePlatforms() {
		name := releaseArchiveName(version, p.goos, p.goarch)
		got, err := checksumForFile(sums, name)
		if err != nil {
			t.Fatalf("published checksums missing %s: %v", name, err)
		}
		seen[name] = got
	}
	if len(seen) != 6 {
		t.Fatalf("want 6 binary assets, got %d", len(seen))
	}

	unknown := releaseArchiveName(version, "plan9", "mips")
	if _, err := checksumForFile(sums, unknown); err == nil {
		t.Fatalf("unknown platform %s must not have a checksum", unknown)
	} else if !strings.Contains(err.Error(), "no checksum entry") {
		t.Fatalf("unknown platform err=%v", err)
	}

	linuxAMD64 := releaseArchiveName(version, "linux", "amd64")
	want, err := checksumForFile(sums, linuxAMD64)
	if err != nil {
		t.Fatal(err)
	}
	tampered := bytes.Replace(sums, []byte(want), []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), 1)
	got, err := checksumForFile(tampered, linuxAMD64)
	if err != nil {
		t.Fatal(err)
	}
	if strings.EqualFold(got, want) {
		t.Fatal("tampered checksums still matched the published linux/amd64 digest")
	}
}

func TestDownloadReleaseUnknownPlatform(t *testing.T) {
	sums := publishedPinnedChecksums(t)
	_, version := normalizeReleaseVersion(ProxyVersion)
	unknownName := releaseArchiveName(version, "plan9", "mips")
	mux := http.NewServeMux()
	mux.HandleFunc("/"+ProxyVersion+"/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(sums)
	})
	mux.HandleFunc("/"+ProxyVersion+"/"+unknownName, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not-a-release-archive"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	_, err := DownloadRelease(context.Background(), DownloadOptions{
		Version:    ProxyVersion,
		DestDir:    t.TempDir(),
		BaseURL:    srv.URL,
		GOOS:       "plan9",
		GOARCH:     "mips",
		HTTPClient: srv.Client(),
	})
	if err == nil || !strings.Contains(err.Error(), "no checksum entry") {
		t.Fatalf("unknown platform err=%v", err)
	}
}

func TestDownloadReleasePublishedChecksumMismatch(t *testing.T) {
	archive, _ := testTarGz(t, "llmentary", []byte("payload"))
	_, version := normalizeReleaseVersion(ProxyVersion)
	archiveName := releaseArchiveName(version, "linux", "amd64")
	sums := publishedPinnedChecksums(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/"+ProxyVersion+"/"+archiveName, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	})
	mux.HandleFunc("/"+ProxyVersion+"/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(sums)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	_, err := DownloadRelease(context.Background(), DownloadOptions{
		Version:    ProxyVersion,
		DestDir:    t.TempDir(),
		BaseURL:    srv.URL,
		GOOS:       "linux",
		GOARCH:     "amd64",
		HTTPClient: srv.Client(),
	})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("published checksum vs fake archive err=%v", err)
	}
}

func pinnedReleasePlatforms() []struct{ goos, goarch, ext string } {
	return []struct{ goos, goarch, ext string }{
		{"darwin", "amd64", ".tar.gz"},
		{"darwin", "arm64", ".tar.gz"},
		{"linux", "amd64", ".tar.gz"},
		{"linux", "arm64", ".tar.gz"},
		{"windows", "amd64", ".zip"},
		{"windows", "arm64", ".zip"},
	}
}

func publishedPinnedChecksums(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "release", "checksums.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		t.Fatal("testdata/release/checksums.txt is empty")
	}
	return data
}

func TestOptionalGitHubToken(t *testing.T) {
	clearGitHubTokens(t)
	t.Setenv("LLMTESTLAB_REPO_TOKEN", "stale-pat")
	if got := optionalGitHubToken(); got != "" {
		t.Fatalf("stale PAT must not be consulted: %q", got)
	}

	t.Setenv("GITHUB_TOKEN", "   ")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("LLMENTARY_GITHUB_TOKEN", "")
	if got := optionalGitHubToken(); got != "" {
		t.Fatalf("whitespace-only GITHUB_TOKEN must be empty: %q", got)
	}

	t.Setenv("GITHUB_TOKEN", "from-github")
	if got := optionalGitHubToken(); got != "from-github" {
		t.Fatalf("GITHUB_TOKEN=%q", got)
	}

	t.Setenv("GH_TOKEN", "from-gh")
	if got := optionalGitHubToken(); got != "from-gh" {
		t.Fatalf("GH_TOKEN should win over GITHUB_TOKEN: %q", got)
	}

	t.Setenv("LLMENTARY_GITHUB_TOKEN", "from-llmentary")
	if got := optionalGitHubToken(); got != "from-llmentary" {
		t.Fatalf("LLMENTARY_GITHUB_TOKEN should win: %q", got)
	}
}

func TestDownloadReleaseAnonymousIgnoresStaleRepoToken(t *testing.T) {
	clearGitHubTokens(t)
	t.Setenv("LLMTESTLAB_REPO_TOKEN", "stale-pat")

	archive, sum := testTarGz(t, "llmentary", []byte("anon-proxy"))
	archiveName := "llmentary_0.1.0_linux_amd64.tar.gz"
	var gotURLs []string
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotURLs = append(gotURLs, req.URL.String())
		if auth := req.Header.Get("Authorization"); auth != "" {
			t.Errorf("anonymous download sent Authorization=%q", auth)
		}
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
	if !bytes.Contains(got, []byte("anon-proxy")) {
		t.Fatalf("contents=%q", got)
	}
	if len(gotURLs) == 0 {
		t.Fatal("expected public download URLs")
	}
	for _, raw := range gotURLs {
		if strings.Contains(raw, "api.github.com") {
			t.Fatalf("anonymous download must not use GitHub API %s", raw)
		}
		if !strings.Contains(raw, "/releases/download/") {
			t.Fatalf("anonymous download must use public release URL, got %s", raw)
		}
	}
}

func TestDownloadReleaseOptionalGitHubTokenUsesAPI(t *testing.T) {
	clearGitHubTokens(t)
	t.Setenv("GITHUB_TOKEN", "rate-limit-token")

	archive, sum := testTarGz(t, "llmentary", []byte("payload"))
	wantArchive := "llmentary_0.1.0_linux_amd64.tar.gz"
	meta := []byte(`{"tag_name":"v0.1.0","assets":[` +
		`{"name":"checksums.txt","url":"https://api.github.com/repos/barzoj-co/llmentary/releases/assets/1"},` +
		`{"name":"` + wantArchive + `","url":"https://api.github.com/repos/barzoj-co/llmentary/releases/assets/2"}` +
		`]}`)
	var gotURLs []string
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotURLs = append(gotURLs, req.URL.String())
		if auth := req.Header.Get("Authorization"); auth != "Bearer rate-limit-token" {
			t.Errorf("Authorization=%q", auth)
		}
		body := []byte("not found")
		status := http.StatusNotFound
		switch {
		case req.URL.Path == "/repos/barzoj-co/llmentary/releases/tags/v0.1.0":
			body = meta
			status = http.StatusOK
		case strings.HasSuffix(req.URL.Path, "/releases/assets/1"):
			body = []byte(sum + "  " + wantArchive + "\n")
			status = http.StatusOK
		case strings.HasSuffix(req.URL.Path, "/releases/assets/2"):
			body = archive
			status = http.StatusOK
		}
		return &http.Response{
			StatusCode: status,
			Body:       io.NopCloser(bytes.NewReader(body)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}
	if _, err := DownloadRelease(context.Background(), DownloadOptions{
		Version:    "v0.1.0",
		DestDir:    t.TempDir(),
		GOOS:       "linux",
		GOARCH:     "amd64",
		HTTPClient: client,
	}); err != nil {
		t.Fatal(err)
	}
	if len(gotURLs) == 0 {
		t.Fatal("expected GitHub API URLs")
	}
	for _, raw := range gotURLs {
		if strings.Contains(raw, "/releases/download/") {
			t.Fatalf("optional token should use API, got %s", raw)
		}
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

func clearGitHubTokens(t *testing.T) {
	t.Helper()
	t.Setenv("LLMENTARY_GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
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
