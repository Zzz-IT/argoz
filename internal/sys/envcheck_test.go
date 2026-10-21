package sys

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestAlpineMissingPackages(t *testing.T) {
	root := t.TempDir()
	if got := AlpineMissingPackages(root); len(got) != 3 {
		t.Fatalf("empty root should miss 3 packages, got %v", got)
	}

	for _, p := range []string{
		"/etc/ssl/certs/ca-certificates.crt",
		"/usr/share/zoneinfo",
	} {
		full := filepath.Join(root, p[1:])
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	loader := "/lib/ld-linux-x86-64.so.2"
	if runtime.GOARCH == "arm64" {
		loader = "/lib/ld-linux-aarch64.so.1"
	}
	if err := os.MkdirAll(filepath.Join(root, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, loader[1:]), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := AlpineMissingPackages(root); len(got) != 0 {
		t.Fatalf("complete root should not miss packages, got %v", got)
	}
}
