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
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	defaultReleaseRepo   = "barzoj-co/llmentary"
	defaultGitHubAPIRoot = "https://api.github.com"
	checksumsName        = "checksums.txt"
	maxArchiveBytes      = 80 << 20
	downloadUserAgent    = "llmentary-go"
	// ProxyVersion is the barzoj-co/llmentary GitHub Release this client
	// pins. Pass it to DownloadRelease unless you intentionally select
	// another tag. The leading v matches the GitHub release tag.
	ProxyVersion = "v0.1.0"
)

// DownloadOptions selects a GitHub Release of barzoj-co/llmentary and
// installs the proxy binary after verifying its checksum.
type DownloadOptions struct {
	// Version is a release tag such as v0.1.0. The leading v is optional.
	// Callers should pass ProxyVersion unless they pin a different release.
	Version string
	// DestDir is the directory that will contain the extracted binary.
	DestDir string
	// Repo is "owner/name". Defaults to barzoj-co/llmentary.
	Repo string
	// Token is an optional GitHub token for private releases.
	// When empty, LLMENTARY_GITHUB_TOKEN, GH_TOKEN, GITHUB_TOKEN, then
	// LLMTESTLAB_REPO_TOKEN are consulted.
	Token string
	// HTTPClient defaults to http.DefaultClient.
	HTTPClient *http.Client
	// BaseURL overrides GitHub download URLs. Intended for tests.
	// When set, assets are fetched from BaseURL/{tag}/{filename}.
	BaseURL string
	// APIBaseURL overrides the GitHub API root. Intended for tests.
	// Defaults to https://api.github.com. Used when a token is set and
	// BaseURL is empty (private GitHub Releases).
	APIBaseURL string
	// GOOS and GOARCH override runtime.GOOS/GOARCH.
	GOOS   string
	GOARCH string
}

// DownloadRelease downloads a pinned llmentary release archive, verifies
// its SHA-256 against checksums.txt, and extracts the binary as llmentary
// (or llmentary.exe on Windows).
func DownloadRelease(ctx context.Context, opts DownloadOptions) (string, error) {
	if strings.TrimSpace(opts.Version) == "" {
		return "", errors.New("llmentary: DownloadRelease requires Version")
	}
	if strings.TrimSpace(opts.DestDir) == "" {
		return "", errors.New("llmentary: DownloadRelease requires DestDir")
	}
	repo := opts.Repo
	if repo == "" {
		repo = defaultReleaseRepo
	}
	goos := opts.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	goarch := opts.GOARCH
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	tag, version := normalizeReleaseVersion(opts.Version)
	archiveName := releaseArchiveName(version, goos, goarch)
	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	token := strings.TrimSpace(opts.Token)
	// Do not send ambient GitHub tokens to a custom BaseURL (tests / mirrors).
	if token == "" && strings.TrimSpace(opts.BaseURL) == "" {
		token = firstNonEmpty(os.Getenv("LLMENTARY_GITHUB_TOKEN"), os.Getenv("GH_TOKEN"), os.Getenv("GITHUB_TOKEN"), os.Getenv("LLMTESTLAB_REPO_TOKEN"))
	}

	sumsBody, archiveBody, err := fetchReleaseAssets(ctx, client, opts.BaseURL, opts.APIBaseURL, repo, tag, archiveName, token)
	if err != nil {
		return "", err
	}
	wantSum, err := checksumForFile(sumsBody, archiveName)
	if err != nil {
		return "", err
	}
	got := sha256.Sum256(archiveBody)
	gotHex := hex.EncodeToString(got[:])
	if !strings.EqualFold(gotHex, wantSum) {
		return "", fmt.Errorf("llmentary: checksum mismatch for %s: got %s want %s", archiveName, gotHex, wantSum)
	}

	if err := os.MkdirAll(opts.DestDir, 0o755); err != nil {
		return "", err
	}
	outName := BinaryName
	if goos == "windows" {
		outName += ".exe"
	}
	outPath := filepath.Join(opts.DestDir, outName)
	data, err := extractReleaseBinary(archiveName, archiveBody)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(outPath, data, 0o755); err != nil {
		return "", err
	}
	return outPath, nil
}

func normalizeReleaseVersion(raw string) (tag, version string) {
	raw = strings.TrimSpace(raw)
	version = strings.TrimPrefix(raw, "v")
	tag = "v" + version
	return tag, version
}

func releaseArchiveName(version, goos, goarch string) string {
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	return "llmentary_" + version + "_" + goos + "_" + goarch + ext
}

func releaseAssetURL(base, repo, tag, name string) string {
	if base != "" {
		return strings.TrimRight(base, "/") + "/" + tag + "/" + name
	}
	return "https://github.com/" + repo + "/releases/download/" + tag + "/" + name
}

func githubReleaseAPIURL(apiRoot, repo, tag string) string {
	apiRoot = strings.TrimRight(apiRoot, "/")
	if apiRoot == "" {
		apiRoot = defaultGitHubAPIRoot
	}
	return apiRoot + "/repos/" + repo + "/releases/tags/" + tag
}

func fetchReleaseAssets(ctx context.Context, client *http.Client, baseURL, apiRoot, repo, tag, archiveName, token string) (sumsBody, archiveBody []byte, err error) {
	if strings.TrimSpace(baseURL) != "" {
		sumsURL := releaseAssetURL(baseURL, repo, tag, checksumsName)
		archiveURL := releaseAssetURL(baseURL, repo, tag, archiveName)
		sumsBody, err = httpGetBytesAccept(ctx, client, sumsURL, token, 1<<20, "application/octet-stream")
		if err != nil {
			return nil, nil, fmt.Errorf("llmentary: download checksums: %w", err)
		}
		archiveBody, err = httpGetBytesAccept(ctx, client, archiveURL, token, maxArchiveBytes, "application/octet-stream")
		if err != nil {
			return nil, nil, fmt.Errorf("llmentary: download archive %s: %w", archiveName, err)
		}
		return sumsBody, archiveBody, nil
	}
	if token != "" {
		return fetchGitHubAPIReleaseAssets(ctx, client, apiRoot, repo, tag, archiveName, token)
	}
	sumsURL := releaseAssetURL("", repo, tag, checksumsName)
	archiveURL := releaseAssetURL("", repo, tag, archiveName)
	sumsBody, err = httpGetBytesAccept(ctx, client, sumsURL, "", 1<<20, "")
	if err != nil {
		return nil, nil, fmt.Errorf("llmentary: download checksums: %w", err)
	}
	archiveBody, err = httpGetBytesAccept(ctx, client, archiveURL, "", maxArchiveBytes, "")
	if err != nil {
		return nil, nil, fmt.Errorf("llmentary: download archive %s: %w", archiveName, err)
	}
	return sumsBody, archiveBody, nil
}

type githubReleaseMeta struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"assets"`
}

func fetchGitHubAPIReleaseAssets(ctx context.Context, client *http.Client, apiRoot, repo, tag, archiveName, token string) (sumsBody, archiveBody []byte, err error) {
	metaURL := githubReleaseAPIURL(apiRoot, repo, tag)
	metaBody, err := httpGetBytesAccept(ctx, client, metaURL, token, 1<<20, "application/vnd.github+json")
	if err != nil {
		return nil, nil, fmt.Errorf("llmentary: lookup release %s: %w", tag, err)
	}
	var meta githubReleaseMeta
	if err := json.Unmarshal(metaBody, &meta); err != nil {
		return nil, nil, fmt.Errorf("llmentary: parse release %s metadata: %w", tag, err)
	}
	var sumsURL, archiveURL string
	var names []string
	for _, a := range meta.Assets {
		names = append(names, a.Name)
		switch a.Name {
		case checksumsName:
			sumsURL = a.URL
		case archiveName:
			archiveURL = a.URL
		}
	}
	if sumsURL == "" || archiveURL == "" {
		return nil, nil, fmt.Errorf("llmentary: release %s missing %s and/or %s (assets: %s)", tag, checksumsName, archiveName, strings.Join(names, ", "))
	}
	sumsBody, err = httpGetBytesAccept(ctx, client, sumsURL, token, 1<<20, "application/octet-stream")
	if err != nil {
		return nil, nil, fmt.Errorf("llmentary: download checksums: %w", err)
	}
	archiveBody, err = httpGetBytesAccept(ctx, client, archiveURL, token, maxArchiveBytes, "application/octet-stream")
	if err != nil {
		return nil, nil, fmt.Errorf("llmentary: download archive %s: %w", archiveName, err)
	}
	return sumsBody, archiveBody, nil
}

func httpGetBytesAccept(ctx context.Context, client *http.Client, rawURL, token string, limit int64, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", downloadUserAgent)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("GET %s: status=%d", redactedURL(rawURL), resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("GET %s: response exceeds %d bytes", redactedURL(rawURL), limit)
	}
	return data, nil
}

func checksumForFile(sums []byte, filename string) (string, error) {
	lines := strings.Split(string(sums), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		sum, file := fields[0], fields[len(fields)-1]
		file = strings.TrimPrefix(file, "*")
		if path.Base(file) == filename {
			if len(sum) != 64 {
				return "", fmt.Errorf("llmentary: unexpected checksum length for %s", filename)
			}
			return strings.ToLower(sum), nil
		}
	}
	return "", fmt.Errorf("llmentary: no checksum entry for %s", filename)
}

func extractReleaseBinary(archiveName string, body []byte) ([]byte, error) {
	wanted := map[string]bool{
		BinaryName:          true,
		BinaryName + ".exe": true,
	}
	if strings.HasSuffix(archiveName, ".zip") {
		return extractZipBinary(body, wanted)
	}
	return extractTarGzBinary(body, wanted)
}

func extractTarGzBinary(body []byte, wanted map[string]bool) ([]byte, error) {
	gr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("llmentary: gunzip archive: %w", err)
	}
	defer func() { _ = gr.Close() }()
	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		name := path.Base(filepath.ToSlash(hdr.Name))
		if strings.Contains(hdr.Name, "..") || !wanted[name] {
			continue
		}
		if hdr.Size > maxArchiveBytes {
			return nil, fmt.Errorf("llmentary: binary %s is too large", name)
		}
		data, err := io.ReadAll(io.LimitReader(tr, hdr.Size))
		if err != nil {
			return nil, err
		}
		return data, nil
	}
	return nil, errors.New("llmentary: archive did not contain llmentary")
}

func extractZipBinary(body []byte, wanted map[string]bool) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return nil, fmt.Errorf("llmentary: unzip archive: %w", err)
	}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := path.Base(filepath.ToSlash(f.Name))
		if strings.Contains(f.Name, "..") || !wanted[name] {
			continue
		}
		if f.UncompressedSize64 > maxArchiveBytes {
			return nil, fmt.Errorf("llmentary: binary %s is too large", name)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(rc, maxArchiveBytes+1))
		_ = rc.Close()
		if err != nil {
			return nil, err
		}
		if int64(len(data)) > maxArchiveBytes {
			return nil, fmt.Errorf("llmentary: binary %s is too large", name)
		}
		return data, nil
	}
	return nil, errors.New("llmentary: zip did not contain llmentary")
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func redactedURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		if i := strings.Index(rawURL, "?"); i >= 0 {
			rawURL = rawURL[:i]
		}
		if at := strings.LastIndex(rawURL, "@"); at >= 0 {
			if scheme := strings.Index(rawURL, "://"); scheme >= 0 && scheme < at {
				return rawURL[:scheme+3] + rawURL[at+1:]
			}
		}
		return rawURL
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}
