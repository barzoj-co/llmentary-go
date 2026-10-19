// SPDX-License-Identifier: MIT

package llmentary

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveBinaryEnvOverride(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "custom-proxy")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvBin, bin)
	got, err := ResolveBinary()
	if err != nil {
		t.Fatal(err)
	}
	if got != bin {
		abs, _ := filepath.Abs(bin)
		if got != abs {
			t.Fatalf("ResolveBinary()=%q want %q", got, bin)
		}
	}
}

func TestResolveBinaryEnvOverrideMissing(t *testing.T) {
	t.Setenv(EnvBin, filepath.Join(t.TempDir(), "does-not-exist"))
	if _, err := ResolveBinary(); err == nil {
		t.Fatal("expected error for missing LLMENTARY_BIN")
	}
}

func TestResolveBinaryPath(t *testing.T) {
	dir := t.TempDir()
	name := BinaryName
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join(dir, name)
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvBin, "")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	got, err := ResolveBinary()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != name {
		t.Fatalf("ResolveBinary()=%q", got)
	}
}

func TestHostport(t *testing.T) {
	if got := hostport(":7777"); got != "127.0.0.1:7777" {
		t.Fatalf("hostport(:7777)=%q", got)
	}
	if got := hostport("127.0.0.1:9"); got != "127.0.0.1:9" {
		t.Fatalf("hostport(127.0.0.1:9)=%q", got)
	}
	if got := hostport("0.0.0.0:8"); got != "127.0.0.1:8" {
		t.Fatalf("hostport(0.0.0.0:8)=%q", got)
	}
}
