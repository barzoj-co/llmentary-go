// SPDX-License-Identifier: MIT

package llmentary

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestProxyConfigTimeoutsAndSpecTargets(t *testing.T) {
	var cfg ProxyConfig
	if err := json.Unmarshal([]byte(`{
		"version":"0.5.0",
		"upstreams":{"openai":"https://api.openai.com/v1"},
		"spec_targets":{"openai":"openai","local":"openai-compatible"},
		"upstream_header_timeout":"2s",
		"upstream_idle_timeout":"500ms"
	}`), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Version != "0.5.0" {
		t.Fatalf("version=%q", cfg.Version)
	}
	if cfg.SpecTarget("openai") != SpecTargetOpenAI || cfg.SpecTarget("local") != SpecTargetOpenAICompatible {
		t.Fatalf("spec_targets=%v", cfg.SpecTargets)
	}
	if cfg.SpecTarget("missing") != "" {
		t.Fatalf("missing target=%q", cfg.SpecTarget("missing"))
	}
	header, ok, err := cfg.HeaderTimeout()
	if err != nil || !ok || header != 2*time.Second {
		t.Fatalf("header=%v ok=%v err=%v", header, ok, err)
	}
	idle, ok, err := cfg.IdleTimeout()
	if err != nil || !ok || idle != 500*time.Millisecond {
		t.Fatalf("idle=%v ok=%v err=%v", idle, ok, err)
	}
}

func TestProxyConfigTimeoutZeroAndAbsent(t *testing.T) {
	var present ProxyConfig
	if err := json.Unmarshal([]byte(`{"upstream_header_timeout":"0s","upstream_idle_timeout":"0s"}`), &present); err != nil {
		t.Fatal(err)
	}
	header, ok, err := present.HeaderTimeout()
	if err != nil || !ok || header != 0 {
		t.Fatalf("present header=%v ok=%v err=%v", header, ok, err)
	}
	idle, ok, err := present.IdleTimeout()
	if err != nil || !ok || idle != 0 {
		t.Fatalf("present idle=%v ok=%v err=%v", idle, ok, err)
	}

	var absent ProxyConfig
	if err := json.Unmarshal([]byte(`{"version":"0.4.1"}`), &absent); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := absent.HeaderTimeout(); err != nil || ok {
		t.Fatalf("absent header ok=%v err=%v", ok, err)
	}
	if _, ok, err := absent.IdleTimeout(); err != nil || ok {
		t.Fatalf("absent idle ok=%v err=%v", ok, err)
	}
	if absent.SpecTarget("openai") != "" {
		t.Fatalf("absent spec target=%q", absent.SpecTarget("openai"))
	}

	var blank ProxyConfig
	if err := json.Unmarshal([]byte(`{"upstream_header_timeout":"  ","upstream_idle_timeout":null}`), &blank); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := blank.HeaderTimeout(); err != nil || ok {
		t.Fatalf("blank header ok=%v err=%v", ok, err)
	}
	if blank.UpstreamIdleTimeout != "" {
		t.Fatalf("null idle raw=%q", blank.UpstreamIdleTimeout)
	}
}

func TestParseConfigDurationCorners(t *testing.T) {
	cases := []struct {
		in      string
		want    time.Duration
		ok      bool
		wantErr bool
	}{
		{in: "", ok: false},
		{in: "   ", ok: false},
		{in: "0s", want: 0, ok: true},
		{in: "0", want: 0, ok: true},
		{in: "2s", want: 2 * time.Second, ok: true},
		{in: " 500ms ", want: 500 * time.Millisecond, ok: true},
		{in: "1.5", want: 1500 * time.Millisecond, ok: true},
		{in: "10m", want: 10 * time.Minute, ok: true},
		{in: "-1s", wantErr: true},
		{in: "-2", wantErr: true},
		{in: "nope", wantErr: true},
	}
	for _, tc := range cases {
		got, ok, err := parseConfigDuration(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("parseConfigDuration(%q) err=nil", tc.in)
			}
			continue
		}
		if err != nil || ok != tc.ok || got != tc.want {
			t.Fatalf("parseConfigDuration(%q)=(%v,%v,%v) want (%v,%v,nil)", tc.in, got, ok, err, tc.want, tc.ok)
		}
	}
}

func TestProxyConfigSpecTargetsShapes(t *testing.T) {
	var empty ProxyConfig
	if err := json.Unmarshal([]byte(`{"spec_targets":{}}`), &empty); err != nil {
		t.Fatal(err)
	}
	if empty.SpecTargets == nil || len(empty.SpecTargets) != 0 || empty.SpecTarget("openai") != "" {
		t.Fatalf("empty spec_targets=%v", empty.SpecTargets)
	}

	var mixed ProxyConfig
	if err := json.Unmarshal([]byte(`{"spec_targets":{"openai":"openai","skip":1,"blank":""}}`), &mixed); err != nil {
		t.Fatal(err)
	}
	if mixed.SpecTarget("openai") != SpecTargetOpenAI || mixed.SpecTarget("skip") != "" || mixed.SpecTarget("blank") != "" {
		t.Fatalf("mixed spec_targets=%v", mixed.SpecTargets)
	}

	var numbersOnly ProxyConfig
	if err := json.Unmarshal([]byte(`{"spec_targets":{"skip":1}}`), &numbersOnly); err != nil {
		t.Fatal(err)
	}
	if numbersOnly.SpecTargets == nil || len(numbersOnly.SpecTargets) != 0 {
		t.Fatalf("numeric-only spec_targets=%v", numbersOnly.SpecTargets)
	}

	var ignored ProxyConfig
	if err := json.Unmarshal([]byte(`{"spec_targets":true}`), &ignored); err != nil {
		t.Fatal(err)
	}
	if ignored.SpecTargets != nil {
		t.Fatalf("true spec_targets=%v", ignored.SpecTargets)
	}

	var nulled ProxyConfig
	if err := json.Unmarshal([]byte(`{"spec_targets":null}`), &nulled); err != nil {
		t.Fatal(err)
	}
	if nulled.SpecTargets != nil {
		t.Fatalf("null spec_targets=%v", nulled.SpecTargets)
	}

	var badTimeout ProxyConfig
	if err := json.Unmarshal([]byte(`{"upstream_header_timeout":1}`), &badTimeout); err == nil {
		t.Fatal("non-string timeout should fail")
	}
}

func TestCallSpecTargetAndTimeoutError(t *testing.T) {
	var openai Call
	if err := json.Unmarshal([]byte(`{
		"id":"c1",
		"upstream_called":true,
		"normalized":{"validation":{"mode":"warn","target":"openai"}}
	}`), &openai); err != nil {
		t.Fatal(err)
	}
	if openai.SpecTarget() != SpecTargetOpenAI || !openai.UpstreamCalled {
		t.Fatalf("openai call=%+v target=%q", openai, openai.SpecTarget())
	}

	var compatible Call
	if err := json.Unmarshal([]byte(`{
		"id":"c2",
		"normalized":{"validation":{"mode":"warn","profile":"relaxed","target":"openai-compatible"}}
	}`), &compatible); err != nil {
		t.Fatal(err)
	}
	if compatible.SpecTarget() != SpecTargetOpenAICompatible {
		t.Fatalf("compatible target=%q", compatible.SpecTarget())
	}

	var absent Call
	if err := json.Unmarshal([]byte(`{"id":"c3"}`), &absent); err != nil {
		t.Fatal(err)
	}
	if absent.SpecTarget() != "" || absent.UpstreamCalled {
		t.Fatalf("absent target=%q called=%v", absent.SpecTarget(), absent.UpstreamCalled)
	}

	var emptyTarget Call
	if err := json.Unmarshal([]byte(`{"normalized":{"validation":{"mode":"warn"}}}`), &emptyTarget); err != nil {
		t.Fatal(err)
	}
	if emptyTarget.SpecTarget() != "" {
		t.Fatalf("empty target=%q", emptyTarget.SpecTarget())
	}

	var headerTimeout Call
	if err := json.Unmarshal([]byte(`{
		"id":"c4",
		"http_status":504,
		"upstream_called":false,
		"normalized":{"error":{"type":"upstream_timeout","code":"upstream_timeout","normalized":"upstream_timeout","source":"proxy","mid_stream":false}}
	}`), &headerTimeout); err != nil {
		t.Fatal(err)
	}
	if headerTimeout.UpstreamCalled {
		t.Fatal("header timeout before write must leave upstream_called false")
	}
	if headerTimeout.Normalized.Error == nil {
		t.Fatal("missing error")
	}
	if headerTimeout.Normalized.Error.Normalized != ErrorNormalizedUpstreamTimeout ||
		headerTimeout.Normalized.Error.Source != ErrorSourceProxy ||
		headerTimeout.Normalized.Error.MidStream {
		t.Fatalf("header timeout error=%+v", headerTimeout.Normalized.Error)
	}

	var idleTimeout Call
	if err := json.Unmarshal([]byte(`{
		"id":"c5",
		"http_status":200,
		"upstream_called":true,
		"normalized":{"stream_truncated":true,"error":{"normalized":"upstream_idle_timeout","source":"proxy","mid_stream":false}}
	}`), &idleTimeout); err != nil {
		t.Fatal(err)
	}
	if !idleTimeout.UpstreamCalled || !idleTimeout.Normalized.StreamTruncated {
		t.Fatalf("idle timeout call=%+v", idleTimeout)
	}
	if idleTimeout.Normalized.Error.Normalized != ErrorNormalizedUpstreamIdleTimeout ||
		idleTimeout.Normalized.Error.Source != ErrorSourceProxy ||
		idleTimeout.Normalized.Error.MidStream {
		t.Fatalf("idle timeout error=%+v", idleTimeout.Normalized.Error)
	}
}

func TestStartTimeoutAndSpecTargetArgs(t *testing.T) {
	cfg := startConfig{}
	WithSpecTarget("openai=" + SpecTargetOpenAICompatible)(&cfg)
	WithUpstreamHeaderTimeout(2 * time.Second)(&cfg)
	WithUpstreamIdleTimeout(0)(&cfg)
	WithAllowPathPrefix("v1beta")(&cfg)
	args := proxyArgs("127.0.0.1:7777", "127.0.0.1:7778", "", nil, cfg.extraArgs)
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"--spec-target", "openai=" + SpecTargetOpenAICompatible,
		"--upstream-header-timeout", "2s",
		"--upstream-idle-timeout", "0s",
		"--allow-path-prefix", "v1beta",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args %v missing %q", args, want)
		}
	}

	sub := startConfig{}
	WithUpstreamHeaderTimeout(250 * time.Microsecond)(&sub)
	WithUpstreamIdleTimeout(-time.Second)(&sub)
	subArgs := proxyArgs("127.0.0.1:7777", "127.0.0.1:7778", "", nil, sub.extraArgs)
	subJoined := strings.Join(subArgs, " ")
	if !strings.Contains(subJoined, "250µs") {
		t.Fatalf("sub-millisecond header timeout lost: %v", subArgs)
	}
	if !strings.Contains(subJoined, "--upstream-idle-timeout") || !strings.Contains(subJoined, "0s") {
		t.Fatalf("negative idle timeout should clamp to 0s: %v", subArgs)
	}
	if formatWaitDuration(250*time.Microsecond) != "0s" {
		t.Fatal("formatWaitDuration must still round sub-ms wait values to 0s")
	}
}
