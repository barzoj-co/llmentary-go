// SPDX-License-Identifier: MIT

// Package llmtestlab is the MIT Go client for llm-test-lab.
//
// Point an OpenAI-compatible SDK at a locally running llm-test-lab proxy,
// then assert on the recorded trace: tool names, arguments, order, retries,
// token usage, latency, and provider errors. The client never uses an LLM
// judge.
//
// The proxy binary lives in github.com/barzoj-co/llm-test-lab. Resolve it
// with LLMTESTLAB_BIN or PATH; see ResolveBinary and DownloadRelease.
package llmtestlab
