// SPDX-License-Identifier: MIT

package llmentary

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
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
	if code, ok := fakeHelperMode(); ok {
		os.Exit(code)
	}
	os.Exit(m.Run())
}

// fakeHelperMode is the TestMain dispatch for portable subprocess fixtures.
// Windows cannot run shebang scripts or /bin/sh. LLMENTARY_FAKE_EXIT wins if both are set.
func fakeHelperMode() (code int, handled bool) {
	if raw := os.Getenv("LLMENTARY_FAKE_EXIT"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return 2, true
		}
		return n, true
	}
	if os.Getenv("LLMENTARY_FAKE_PROXY") == "1" {
		return runFakeProxy(), true
	}
	return 0, false
}

func runFakeProxy() int {
	control, token := parseFakeProxyArgs(os.Args[1:])
	status, err := fakeHealthStatus()
	if err != nil {
		return 2
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
	addr, err := rewriteLoopbackHost(control)
	if err != nil {
		return 2
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", fakeHealthz(token, status))
	if err := http.ListenAndServe(addr, mux); err != nil {
		return 1
	}
	return 0
}

func parseFakeProxyArgs(args []string) (control, token string) {
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
	return control, token
}

func fakeHealthStatus() (int, error) {
	raw := os.Getenv("FAKE_HEALTH_STATUS")
	if raw == "" {
		return 200, nil
	}
	return strconv.Atoi(raw)
}

func rewriteLoopbackHost(addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", err
	}
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port), nil
}

func fakeHealthz(token string, status int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if token != "" && r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte("ok"))
	}
}

func fakeProxyBin() string {
	bin, err := filepath.Abs(os.Args[0])
	if err != nil {
		return os.Args[0]
	}
	return bin
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
	gotOut, gotErr = p.Output()
	if gotOut != stdout.String() || gotErr != stderr.String() {
		t.Fatalf("Output mismatch stdout=%q stderr=%q", gotOut, gotErr)
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
		return runFakeHelper(t, []string{"LLMENTARY_FAKE_EXIT=" + strconv.Itoa(code)}).Run()
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

func TestFakeHelperModeExitCodes(t *testing.T) {
	t.Setenv("LLMENTARY_FAKE_PROXY", "")
	t.Setenv("LLMENTARY_FAKE_EXIT", "")
	if code, ok := fakeHelperMode(); ok || code != 0 {
		t.Fatalf("idle helper mode code=%d ok=%v", code, ok)
	}

	t.Setenv("LLMENTARY_FAKE_EXIT", "130")
	code, ok := fakeHelperMode()
	if !ok || code != 130 {
		t.Fatalf("exit 130: code=%d ok=%v", code, ok)
	}

	t.Setenv("LLMENTARY_FAKE_EXIT", "0")
	code, ok = fakeHelperMode()
	if !ok || code != 0 {
		t.Fatalf("exit 0: code=%d ok=%v", code, ok)
	}

	t.Setenv("LLMENTARY_FAKE_EXIT", "not-a-number")
	code, ok = fakeHelperMode()
	if !ok || code != 2 {
		t.Fatalf("invalid exit: code=%d ok=%v", code, ok)
	}

	t.Setenv("LLMENTARY_FAKE_EXIT", "7")
	t.Setenv("LLMENTARY_FAKE_PROXY", "1")
	code, ok = fakeHelperMode()
	if !ok || code != 7 {
		t.Fatalf("exit wins over proxy: code=%d ok=%v", code, ok)
	}
}

func TestParseFakeProxyArgs(t *testing.T) {
	t.Parallel()
	control, token := parseFakeProxyArgs([]string{
		"--listen", "127.0.0.1:1",
		"--control", "127.0.0.1:2",
		"--control-token", "secret",
		"--upstream", "openai=https://example.test",
		"--help-ignore",
	})
	if control != "127.0.0.1:2" || token != "secret" {
		t.Fatalf("control=%q token=%q", control, token)
	}

	control, token = parseFakeProxyArgs([]string{"--control"})
	if control != "" || token != "" {
		t.Fatalf("dangling --control: control=%q token=%q", control, token)
	}

	control, token = parseFakeProxyArgs(nil)
	if control != "" || token != "" {
		t.Fatalf("empty args: control=%q token=%q", control, token)
	}
}

func TestRewriteLoopbackHost(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"127.0.0.1:9", "127.0.0.1:9"},
		{"0.0.0.0:9", "127.0.0.1:9"},
		{":9", "127.0.0.1:9"},
		{"[::1]:9", "[::1]:9"},
	}
	for _, tc := range cases {
		got, err := rewriteLoopbackHost(tc.in)
		if err != nil || got != tc.want {
			t.Fatalf("rewriteLoopbackHost(%q)=%q %v want %q", tc.in, got, err, tc.want)
		}
	}
	if _, err := rewriteLoopbackHost("not-a-hostport"); err == nil {
		t.Fatal("expected error for missing port")
	}
}

func TestFakeHealthStatusAndHandler(t *testing.T) {
	t.Setenv("FAKE_HEALTH_STATUS", "")
	got, err := fakeHealthStatus()
	if err != nil || got != 200 {
		t.Fatalf("default status=%d err=%v", got, err)
	}
	t.Setenv("FAKE_HEALTH_STATUS", "503")
	got, err = fakeHealthStatus()
	if err != nil || got != 503 {
		t.Fatalf("503 status=%d err=%v", got, err)
	}
	t.Setenv("FAKE_HEALTH_STATUS", "nope")
	if _, err = fakeHealthStatus(); err == nil {
		t.Fatal("expected parse error")
	}

	handler := fakeHealthz("secret", 200)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status=%d", rec.Code)
	}
	req.Header.Set("Authorization", "Bearer wrong")
	rec = httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token status=%d", rec.Code)
	}
	req.Header.Set("Authorization", "Bearer secret")
	rec = httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != 200 || rec.Body.String() != "ok" {
		t.Fatalf("authed status=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestFakeProxyBinIsTestExecutable(t *testing.T) {
	bin := fakeProxyBin()
	if !filepath.IsAbs(bin) {
		t.Fatalf("fakeProxyBin is not absolute: %q", bin)
	}
	info, err := os.Stat(bin)
	if err != nil {
		t.Fatal(err)
	}
	if info.IsDir() {
		t.Fatalf("fakeProxyBin is a directory: %q", bin)
	}
	switch strings.ToLower(filepath.Ext(bin)) {
	case ".sh", ".bash", ".py", ".ps1", ".bat", ".cmd":
		t.Fatalf("fakeProxyBin looks like a shell script: %q", bin)
	}
	abs, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if bin != abs {
		t.Fatalf("fakeProxyBin=%q want %q", bin, abs)
	}
}

func TestFakeHelperSubprocessCornerCases(t *testing.T) {
	err := runFakeHelper(t, []string{"LLMENTARY_FAKE_EXIT=not-a-number"}).Run()
	if got := exitStatus(err); got != 2 {
		t.Fatalf("invalid FAKE_EXIT status=%d err=%v", got, err)
	}

	err = runFakeHelper(t, []string{"LLMENTARY_FAKE_EXIT=0"}).Run()
	if err != nil {
		t.Fatalf("FAKE_EXIT=0: %v", err)
	}

	err = runFakeHelper(t, []string{"LLMENTARY_FAKE_PROXY=1"}).Run()
	if got := exitStatus(err); got != 2 {
		t.Fatalf("proxy without --control status=%d err=%v", got, err)
	}

	err = runFakeHelper(t, []string{"LLMENTARY_FAKE_PROXY=1", "FAKE_HEALTH_STATUS=nope"}, "--control", "127.0.0.1:9").Run()
	if got := exitStatus(err); got != 2 {
		t.Fatalf("invalid FAKE_HEALTH_STATUS status=%d err=%v", got, err)
	}
}

func TestFakeHealthUnauthorizedViaStart(t *testing.T) {
	t.Setenv("LLMENTARY_FAKE_PROXY", "1")
	p, err := Start(context.Background(),
		WithBinary(fakeProxyBin()),
		WithStartTimeout(2*time.Second),
		WithStartControlToken("secret"),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Stop() })

	resp, err := http.Get(p.ControlURL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated healthz=%d", resp.StatusCode)
	}
}

func runFakeHelper(t *testing.T, extraEnv []string, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(fakeProxyBin(), args...)
	cmd.Env = append(os.Environ(), extraEnv...)
	return cmd
}

func exitStatus(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

type errString string

func (e errString) Error() string { return string(e) }
