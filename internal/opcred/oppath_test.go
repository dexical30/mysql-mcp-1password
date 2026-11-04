package opcred

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveOpPath_RejectsRelative(t *testing.T) {
	for _, p := range []string{"op", "./op"} {
		if _, err := ResolveOpPath(p); err == nil {
			t.Errorf("ResolveOpPath(%q): want error", p)
		}
	}
}

func TestResolveOpPath_RejectsUnsigned(t *testing.T) {
	p := filepath.Join(t.TempDir(), "op")
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho fake\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveOpPath(p); err == nil {
		t.Fatal("unsigned fake op was accepted")
	}
}

func TestResolveOpPath_RealOp(t *testing.T) {
	found := false
	for _, p := range defaultOpPaths {
		if _, err := os.Stat(p); err == nil {
			found = true
		}
	}
	if !found {
		t.Skip("1Password CLI not installed in a default location")
	}
	p, err := ResolveOpPath("")
	if err != nil {
		t.Fatalf("ResolveOpPath: %v", err)
	}
	if !filepath.IsAbs(p) {
		t.Fatalf("resolved path %q is not absolute", p)
	}
}
