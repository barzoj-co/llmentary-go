// SPDX-License-Identifier: MIT

// Package llmentary is the MIT Go client for llmentary: deterministic
// trace-based integration tests for LLM apps.
//
// Point an OpenAI-compatible SDK at a locally running llmentary proxy,
// then assert on the recorded trace: tool names, arguments, order, retries,
// token usage, latency, and provider errors. The client never uses an LLM
// judge.
//
// The proxy binary lives in github.com/barzoj-co/llmentary. Resolve it
// with LLMENTARY_BIN or PATH; see ResolveBinary and DownloadRelease.
package llmentary
