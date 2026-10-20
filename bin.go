// SPDX-License-Identifier: MIT

package llmentary

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	// BinaryName is the PATH name and the file name inside GitHub Release archives.
	BinaryName = "llmentary"
	// EnvBin is the environment variable that overrides binary resolution.
	EnvBin = "LLMENTARY_BIN"
)

// ResolveBinary returns the llmentary proxy executable.
//
// Resolution order:
//  1. LLMENTARY_BIN, if set (must exist and be executable-looking)
//  2. llmentary on PATH (GitHub Releases / `go install` name)
func ResolveBinary() (string, error) {
	if override := strings.TrimSpace(os.Getenv(EnvBin)); override != "" {
		path, err := filepath.Abs(override)
		if err != nil {
			return "", fmt.Errorf("LLMENTARY_BIN %q: %w", override, err)
		}
		info, err := os.Stat(path)
		if err != nil {
			return "", fmt.Errorf("LLMENTARY_BIN %q: %w", override, err)
		}
		if info.IsDir() {
			return "", fmt.Errorf("LLMENTARY_BIN %q is a directory", override)
		}
		return path, nil
	}
	if path, err := exec.LookPath(BinaryName); err == nil {
		return path, nil
	}
	if runtime.GOOS == "windows" {
		if path, err := exec.LookPath(BinaryName + ".exe"); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("llmentary binary not found: set %s or install %s on PATH (see https://github.com/barzoj-co/llmentary)", EnvBin, BinaryName)
}

type startConfig struct {
	bin          string
	listenAddr   string
	controlAddr  string
	controlToken string
	upstreams    []string
	extraArgs    []string
	dir          string
	timeout      time.Duration
	httpClient   *http.Client
	stdout       io.Writer
	stderr       io.Writer
}

// StartOption configures Start / StartForTest.
type StartOption func(*startConfig)

// WithBinary sets the proxy executable path, skipping ResolveBinary.
func WithBinary(path string) StartOption {
	return func(c *startConfig) { c.bin = path }
}

// WithListenAddr sets the proxy listen address (default: an ephemeral 127.0.0.1 port).
func WithListenAddr(addr string) StartOption {
	return func(c *startConfig) { c.listenAddr = addr }
}

// WithControlAddr sets the control API listen address (default: an ephemeral 127.0.0.1 port).
func WithControlAddr(addr string) StartOption {
	return func(c *startConfig) { c.controlAddr = addr }
}

// WithStartControlToken sets --control-token for the proxy process.
func WithStartControlToken(token string) StartOption {
	return func(c *startConfig) { c.controlToken = token }
}

// WithUpstream adds a --upstream name=url mapping. Optional: with none,
// the proxy defaults to openai=https://api.openai.com/v1.
func WithUpstream(spec string) StartOption {
	return func(c *startConfig) { c.upstreams = append(c.upstreams, spec) }
}

// WithExtraArgs appends extra CLI arguments.
func WithExtraArgs(args ...string) StartOption {
	return func(c *startConfig) { c.extraArgs = append(c.extraArgs, args...) }
}

// WithStartDir sets the process working directory.
func WithStartDir(dir string) StartOption {
	return func(c *startConfig) { c.dir = dir }
}

// WithStartTimeout sets how long Start waits for /healthz (default 10s).
func WithStartTimeout(d time.Duration) StartOption {
	return func(c *startConfig) { c.timeout = d }
}

// WithStartHTTPClient sets the client used to poll /healthz.
func WithStartHTTPClient(client *http.Client) StartOption {
	return func(c *startConfig) { c.httpClient = client }
}

// WithStartOutput captures proxy stdout/stderr (optional).
func WithStartOutput(stdout, stderr io.Writer) StartOption {
	return func(c *startConfig) {
		c.stdout = stdout
		c.stderr = stderr
	}
}

// syncedBuffer is a bytes.Buffer safe for concurrent Write and String.
type syncedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncedBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncedBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// Proxy is a running llmentary proxy process started by this client.
type Proxy struct {
	Bin        string
	ProxyURL   string
	ControlURL string
	cmd        *exec.Cmd
	stdout     *syncedBuffer
	stderr     *syncedBuffer
	stopOnce   sync.Once
	stopErr    error
}

// Start launches the proxy binary and waits until the control API is healthy.
func Start(ctx context.Context, opts ...StartOption) (*Proxy, error) {
	cfg := startConfig{timeout: 10 * time.Second}
	for _, opt := range opts {
		opt(&cfg)
	}
	bin := cfg.bin
	if bin == "" {
		var err error
		bin, err = ResolveBinary()
		if err != nil {
			return nil, err
		}
	}
	listenAddr := cfg.listenAddr
	if listenAddr == "" {
		addr, err := freeLoopbackAddr()
		if err != nil {
			return nil, err
		}
		listenAddr = addr
	}
	controlAddr := cfg.controlAddr
	if controlAddr == "" {
		addr, err := freeLoopbackAddr()
		if err != nil {
			return nil, err
		}
		controlAddr = addr
	}

	args := proxyArgs(listenAddr, controlAddr, cfg.controlToken, cfg.upstreams, cfg.extraArgs)

	// Do not bind the process to ctx: ctx only covers the health wait.
	cmd := exec.Command(bin, args...)
	cmd.Dir = cfg.dir
	stdout := &syncedBuffer{}
	stderr := &syncedBuffer{}
	cmd.Stdout = io.MultiWriter(stdout, discardIfNil(cfg.stdout))
	cmd.Stderr = io.MultiWriter(stderr, discardIfNil(cfg.stderr))
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("llmentary: start %s: %w", bin, err)
	}

	p := &Proxy{
		Bin:        bin,
		ProxyURL:   "http://" + hostport(listenAddr),
		ControlURL: "http://" + hostport(controlAddr),
		cmd:        cmd,
		stdout:     stdout,
		stderr:     stderr,
	}

	waitCtx := ctx
	if cfg.timeout > 0 {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, cfg.timeout)
		defer cancel()
	}
	httpClient := cfg.httpClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 500 * time.Millisecond}
	}
	if err := waitHealthy(waitCtx, httpClient, p.ControlURL+"/healthz", cfg.controlToken); err != nil {
		_ = p.Stop()
		return nil, fmt.Errorf("llmentary: proxy did not become healthy: %w\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	return p, nil
}

// StartForTest starts the proxy and registers t.Cleanup to stop it.
func StartForTest(t testing.TB, opts ...StartOption) *Proxy {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	p, err := Start(ctx, opts...)
	if err != nil {
		t.Fatalf("llmentary: start proxy: %v", err)
	}
	t.Cleanup(func() {
		if err := p.Stop(); err != nil {
			t.Errorf("llmentary: stop proxy: %v", err)
		}
	})
	return p
}

// Stop terminates the proxy process.
func (p *Proxy) Stop() error {
	if p == nil {
		return nil
	}
	p.stopOnce.Do(func() {
		if p.cmd == nil || p.cmd.Process == nil {
			return
		}
		done := make(chan error, 1)
		go func() { done <- p.cmd.Wait() }()
		sigErr := p.cmd.Process.Signal(os.Interrupt)
		if sigErr != nil {
			p.stopErr = finishStop(p.cmd, done, true)
			return
		}
		select {
		case err := <-done:
			if err != nil && !isExpectedExit(err, false) {
				p.stopErr = err
			}
		case <-time.After(2 * time.Second):
			p.stopErr = finishStop(p.cmd, done, true)
		}
	})
	return p.stopErr
}

func finishStop(cmd *exec.Cmd, done <-chan error, killed bool) error {
	killErr := cmd.Process.Kill()
	waitErr := <-done
	if waitErr != nil && !isExpectedExit(waitErr, killed) {
		if killErr != nil {
			return fmt.Errorf("kill: %v (wait: %v)", killErr, waitErr)
		}
		return waitErr
	}
	if waitErr == nil && killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
		return killErr
	}
	return nil
}

// Output returns captured stdout and stderr. Do not log this if it might
// contain secrets; the proxy redacts recorded traces but process logs may
// still mention configured addresses.
func (p *Proxy) Output() (stdout, stderr string) {
	if p == nil {
		return "", ""
	}
	return p.stdout.String(), p.stderr.String()
}

func waitHealthy(ctx context.Context, client *http.Client, healthURL, token string) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	var last error
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
		if err != nil {
			return err
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode <= 299 {
				return nil
			}
			last = fmt.Errorf("healthz status %d", resp.StatusCode)
		} else {
			last = err
		}
		select {
		case <-ctx.Done():
			if last == nil {
				last = ctx.Err()
			}
			return last
		case <-ticker.C:
		}
	}
}

func freeLoopbackAddr() (string, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer func() { _ = l.Close() }()
	return l.Addr().String(), nil
}

func proxyArgs(listenAddr, controlAddr, token string, upstreams, extra []string) []string {
	args := []string{
		"--listen", listenAddr,
		"--control", controlAddr,
	}
	if token != "" {
		args = append(args, "--control-token", token)
	}
	for _, spec := range upstreams {
		args = append(args, "--upstream", spec)
	}
	return append(args, extra...)
}

func hostport(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "127.0.0.1" + addr
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

func discardIfNil(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}

func isExpectedExit(err error, killed bool) bool {
	if err == nil {
		return true
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		status := exitErr.ExitCode()
		if status == 0 || status == -1 || status == 130 || status == 143 {
			return true
		}
		// Windows Kill often yields exit status 1.
		if killed && status == 1 {
			return true
		}
	}
	msg := err.Error()
	return strings.Contains(msg, "signal: killed") || strings.Contains(msg, "signal: interrupt")
}
