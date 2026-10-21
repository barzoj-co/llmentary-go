// SPDX-License-Identifier: MIT

package llmentary

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFakeProxy(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "llmentary")
	script := `#!/usr/bin/env python3
import os, sys
from http.server import BaseHTTPRequestHandler, HTTPServer

control = token = ""
args = sys.argv[1:]
i = 0
while i < len(args):
    if args[i] == "--control" and i + 1 < len(args):
        control = args[i + 1]
        i += 2
        continue
    if args[i] == "--control-token" and i + 1 < len(args):
        token = args[i + 1]
        i += 2
        continue
    if args[i] in ("--listen", "--upstream") and i + 1 < len(args):
        i += 2
        continue
    i += 1

status = int(os.environ.get("FAKE_HEALTH_STATUS", "200"))
if os.environ.get("FAKE_IGNORE_INT") == "1":
    import signal
    signal.signal(signal.SIGINT, signal.SIG_IGN)
marker = os.environ.get("FAKE_MARKER", "")
if marker:
    with open(marker, "w", encoding="utf-8") as fh:
        fh.write(os.getcwd())

class H(BaseHTTPRequestHandler):
    def do_GET(self):
        if token and self.headers.get("Authorization") != "Bearer " + token:
            self.send_response(401)
            self.end_headers()
            return
        if self.path.endswith("/healthz"):
            self.send_response(status)
            self.end_headers()
            self.wfile.write(b"ok")
            return
        self.send_response(404)
        self.end_headers()

    def log_message(self, *_args):
        pass

host, _, port = control.rpartition(":")
if host in ("", "0.0.0.0"):
    host = "127.0.0.1"
HTTPServer((host, int(port)), H).serve_forever()
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestStartForTestHealthyFake(t *testing.T) {
	bin := writeFakeProxy(t)
	dir := t.TempDir()
	marker := filepath.Join(dir, "started")
	t.Setenv("FAKE_MARKER", marker)
	t.Setenv("FAKE_HEALTH_STATUS", "200")

	var stdout, stderr bytes.Buffer
	p := StartForTest(t,
		WithBinary(bin),
		WithStartDir(dir),
		WithStartTimeout(2*time.Second),
		WithStartControlToken("secret"),
		WithUpstream("openai=https://example.test"),
		WithExtraArgs("--help-ignore"),
		WithStartHTTPClient(&http.Client{Timeout: time.Second}),
		WithStartOutput(&stdout, &stderr),
	)
	if p.Bin != bin {
		t.Fatalf("Bin=%q", p.Bin)
	}
	if !strings.HasPrefix(p.ProxyURL, "http://127.0.0.1:") || !strings.HasPrefix(p.ControlURL, "http://127.0.0.1:") {
		t.Fatalf("urls proxy=%s control=%s", p.ProxyURL, p.ControlURL)
	}
	gotOut, gotErr := p.Output()
	if gotOut != stdout.String() || gotErr != stderr.String() {
		t.Fatalf("Output mismatch stdout=%q stderr=%q", gotOut, gotErr)
	}
	cwd, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(cwd) != dir {
		t.Fatalf("working dir=%q want %q", cwd, dir)
	}
	if err := p.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := p.Stop(); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
}

func TestStopKillsInterruptResistantFake(t *testing.T) {
	t.Setenv("FAKE_IGNORE_INT", "1")
	bin := writeFakeProxy(t)
	p, err := Start(context.Background(), WithBinary(bin), WithStartTimeout(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Stop(); err != nil {
		t.Fatalf("Stop after ignored interrupt: %v", err)
	}
}

func TestStartHealthTimeoutAndMissingBinary(t *testing.T) {
	t.Setenv("FAKE_HEALTH_STATUS", "503")
	bin := writeFakeProxy(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := Start(ctx, WithBinary(bin), WithStartTimeout(200*time.Millisecond))
	if err == nil || !strings.Contains(err.Error(), "did not become healthy") {
		t.Fatalf("unhealthy start err=%v", err)
	}

	_, err = Start(ctx, WithBinary(filepath.Join(t.TempDir(), "missing")))
	if err == nil || !strings.Contains(err.Error(), "start") {
		t.Fatalf("missing binary err=%v", err)
	}
}

func TestStartResolvesBinaryAndNilProxy(t *testing.T) {
	bin := writeFakeProxy(t)
	t.Setenv(EnvBin, bin)
	p, err := Start(context.Background(), WithStartTimeout(2*time.Second), WithListenAddr(""), WithControlAddr(""))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Stop() })
	if p.Bin != bin && filepath.Base(p.Bin) != filepath.Base(bin) {
		abs, _ := filepath.Abs(bin)
		if p.Bin != abs {
			t.Fatalf("resolved Bin=%q", p.Bin)
		}
	}
	var none *Proxy
	if err := none.Stop(); err != nil {
		t.Fatalf("nil Stop: %v", err)
	}
	out, errOut := none.Output()
	if out != "" || errOut != "" {
		t.Fatalf("nil Output=%q %q", out, errOut)
	}
}

func TestResolveBinaryMissingFromPath(t *testing.T) {
	t.Setenv(EnvBin, "")
	t.Setenv("PATH", t.TempDir())
	if _, err := ResolveBinary(); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("empty PATH err=%v", err)
	}
}

func TestResolveBinaryDirectoryAndHostportV6(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvBin, dir)
	if _, err := ResolveBinary(); err == nil || !strings.Contains(err.Error(), "directory") {
		t.Fatalf("directory override err=%v", err)
	}
	if got := hostport("[::]:9"); got != "127.0.0.1:9" {
		t.Fatalf("hostport([::]:9)=%q", got)
	}
	if discardIfNil(nil) == nil {
		t.Fatal("discardIfNil(nil)")
	}
}

func TestIsExpectedExitCodes(t *testing.T) {
	if !isExpectedExit(nil, false) {
		t.Fatal("nil is expected")
	}
	cmd := exec.Command("/bin/sh", "-c", "exit 130")
	err := cmd.Run()
	if !isExpectedExit(err, false) {
		t.Fatalf("exit 130 should be expected: %v", err)
	}
	cmd = exec.Command("/bin/sh", "-c", "exit 1")
	err = cmd.Run()
	if isExpectedExit(err, false) {
		t.Fatalf("exit 1 without kill should not be expected: %v", err)
	}
	if !isExpectedExit(err, true) {
		t.Fatalf("exit 1 with kill should be expected: %v", err)
	}
	if !isExpectedExit(errString("signal: killed"), false) || !isExpectedExit(errString("signal: interrupt"), true) {
		t.Fatal("signal text should be expected")
	}
}

type errString string

func (e errString) Error() string { return string(e) }
