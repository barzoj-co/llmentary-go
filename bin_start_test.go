// SPDX-License-Identifier: MIT

package llmentary

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if code := os.Getenv("LLMENTARY_FAKE_EXIT"); code != "" {
		n, err := strconv.Atoi(code)
		if err != nil {
			os.Exit(2)
		}
		os.Exit(n)
	}
	if os.Getenv("LLMENTARY_FAKE_PROXY") == "1" {
		os.Exit(runFakeProxy())
	}
	os.Exit(m.Run())
}

func runFakeProxy() int {
	control, token := "", ""
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		if i+1 >= len(args) {
			continue
		}
		switch args[i] {
		case "--control":
			control = args[i+1]
			i++
		case "--control-token":
			token = args[i+1]
			i++
		case "--listen", "--upstream":
			i++
		}
	}
	status := 200
	if raw := os.Getenv("FAKE_HEALTH_STATUS"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return 2
		}
		status = n
	}
	if os.Getenv("FAKE_IGNORE_INT") == "1" {
		signal.Ignore(os.Interrupt)
	}
	if marker := os.Getenv("FAKE_MARKER"); marker != "" {
		cwd, err := os.Getwd()
		if err != nil {
			return 2
		}
		if err := os.WriteFile(marker, []byte(cwd), 0o644); err != nil {
			return 2
		}
	}
	_, _ = os.Stdout.Write([]byte("fake-proxy-start\n"))
	_, _ = os.Stderr.Write([]byte("fake-proxy-ready\n"))
	host, port, err := net.SplitHostPort(control)
	if err != nil {
		return 2
	}
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if token != "" && r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte("ok"))
	})
	if err := http.ListenAndServe(net.JoinHostPort(host, port), mux); err != nil {
		return 1
	}
	return 0
}

func fakeProxyBin() string {
	return os.Args[0]
}

func TestStartForTestHealthyFake(t *testing.T) {
	t.Setenv("LLMENTARY_FAKE_PROXY", "1")
	bin := fakeProxyBin()
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
	if !strings.Contains(gotOut, "fake-proxy-start") || !strings.Contains(gotErr, "fake-proxy-ready") {
		t.Fatalf("Output stdout=%q stderr=%q", gotOut, gotErr)
	}
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
	t.Setenv("LLMENTARY_FAKE_PROXY", "1")
	t.Setenv("FAKE_IGNORE_INT", "1")
	p, err := Start(context.Background(), WithBinary(fakeProxyBin()), WithStartTimeout(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Stop(); err != nil {
		t.Fatalf("Stop after ignored interrupt: %v", err)
	}
}

func TestStartHealthTimeoutAndMissingBinary(t *testing.T) {
	t.Setenv("LLMENTARY_FAKE_PROXY", "1")
	t.Setenv("FAKE_HEALTH_STATUS", "503")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := Start(ctx, WithBinary(fakeProxyBin()), WithStartTimeout(200*time.Millisecond))
	if err == nil || !strings.Contains(err.Error(), "did not become healthy") {
		t.Fatalf("unhealthy start err=%v", err)
	}

	_, err = Start(ctx, WithBinary(filepath.Join(t.TempDir(), "missing")))
	if err == nil || !strings.Contains(err.Error(), "start") {
		t.Fatalf("missing binary err=%v", err)
	}
}

func TestStartResolvesBinaryAndNilProxy(t *testing.T) {
	t.Setenv("LLMENTARY_FAKE_PROXY", "1")
	bin := fakeProxyBin()
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
	runExit := func(code int) error {
		cmd := exec.Command(os.Args[0], "-test.run=^$")
		cmd.Env = append(os.Environ(), "LLMENTARY_FAKE_EXIT="+strconv.Itoa(code))
		return cmd.Run()
	}
	if err := runExit(130); !isExpectedExit(err, false) {
		t.Fatalf("exit 130 should be expected: %v", err)
	}
	err := runExit(1)
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
