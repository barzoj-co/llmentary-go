// SPDX-License-Identifier: MIT

package llmentary

import (
	"strings"
	"testing"
)

func TestStartOmitsUpstreamWhenUnset(t *testing.T) {
	args := proxyArgs("127.0.0.1:7777", "127.0.0.1:7778", "", nil, nil)
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--upstream") {
		t.Fatalf("default start args should omit --upstream so the proxy uses openai=https://api.openai.com/v1: %v", args)
	}
	if !strings.Contains(joined, "--listen") || !strings.Contains(joined, "--control") {
		t.Fatalf("args=%v", args)
	}
}

func TestStartKeepsExplicitUpstream(t *testing.T) {
	args := proxyArgs("127.0.0.1:7777", "127.0.0.1:7778", "tok", []string{"openai=https://api.openai.com/v1"}, nil)
	joined := strings.Join(args, " ")
	for _, want := range []string{"--upstream", "openai=https://api.openai.com/v1", "--control-token", "tok"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args %v missing %q", args, want)
		}
	}
}
