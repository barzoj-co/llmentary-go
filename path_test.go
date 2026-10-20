// SPDX-License-Identifier: MIT

package llmentary

import "testing"

func TestPathBaseURLTable(t *testing.T) {
	const proxy = "http://127.0.0.1:7777"
	// Shared with Python path_base_url / JS pathBaseURL.
	cases := []struct {
		session  string
		upstream string
		want     string
	}{
		{"t1", "", proxy + "/s/t1/v1"},
		{"t 1", "openrouter", proxy + "/s/t%201/u/openrouter"},
		{"abc", "openai", proxy + "/s/abc/u/openai"},
		{"id/x", "", proxy + "/s/id%2Fx/v1"},
	}
	for _, tc := range cases {
		lm := &Session{proxyURL: proxy, sessionID: tc.session}
		if got := lm.PathBaseURL(tc.upstream); got != tc.want {
			t.Fatalf("PathBaseURL(%q,%q)=%q want %q", tc.session, tc.upstream, got, tc.want)
		}
	}
}

func TestWithSessionID(t *testing.T) {
	lm := New(t,
		WithProxyURL("http://127.0.0.1:7777"),
		WithControlURL("http://127.0.0.1:7778"),
		WithSessionID("fixed-id"),
	)
	if lm.SessionID() != "fixed-id" {
		t.Fatalf("session=%q", lm.SessionID())
	}
	if got := lm.PathBaseURL(""); got != "http://127.0.0.1:7777/s/fixed-id/v1" {
		t.Fatalf("PathBaseURL=%q", got)
	}
}
