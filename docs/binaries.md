# Proxy binary resolution

Users install the recording proxy from
[barzoj-co/llmentary](https://github.com/barzoj-co/llmentary) GitHub Releases, or
`go install github.com/barzoj-co/llmentary/cmd/llmentary@latest`, and put it on
`PATH` as `llmentary` (or set `LLMENTARY_BIN`). This page is the client lookup
and download behavior used by tests and `Start` / `StartForTest`.

## Lookup order

`ResolveBinary` looks up the proxy executable in this order:

1. `LLMENTARY_BIN` (absolute or relative path; must exist and not be a directory)
2. `llmentary` on `PATH` (GitHub Releases / `go install` name; `llmentary.exe` on Windows)

There is no CGO and no post-install hook.

```go
path, err := llmentary.ResolveBinary()
proxy, err := llmentary.Start(ctx,
    llmentary.WithBinary(path),
    // WithUpstream is optional; the proxy defaults to openai=https://api.openai.com/v1.
)
```

## DownloadRelease

`DownloadRelease` fetches a pinned GitHub Release of `barzoj-co/llmentary`
(`ProxyVersion`, currently `v0.5.1`) and verifies `checksums.txt` before
extracting the `llmentary` binary.

```go
path, err := llmentary.DownloadRelease(ctx, llmentary.DownloadOptions{
    Version: llmentary.ProxyVersion,
    DestDir: ".llmentary",
})
proxy, err := llmentary.Start(ctx,
    llmentary.WithBinary(path),
)
```

Pass `DownloadOptions.Version` as `ProxyVersion` unless you intentionally pin
another tag. Optional `DownloadOptions.Token` is a GitHub rate-limit assist
for public Release assets; a token is never required. When empty the client
consults `LLMENTARY_GITHUB_TOKEN`, `GH_TOKEN`, then `GITHUB_TOKEN`.

See [GitHub Actions and CI](ci.md) for how CI installs the pinned release.
