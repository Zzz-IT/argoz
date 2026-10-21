package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCompareVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v1.2.0", "1.2.0", 0},
		{"v1.2.1", "v1.2.0", 1},
		{"v1.2.0", "v1.10.0", -1},
		{"v2", "v1.9.9", 1},
		{"dev", "v1.2.0", -1},
	}
	for _, tc := range cases {
		if got := compareVersion(tc.a, tc.b); got != tc.want {
			t.Fatalf("compareVersion(%q,%q)=%d want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestParseChecksums(t *testing.T) {
	data := "abc123  argoz-amd64\ndef456 *argoz-arm64\n"
	got := parseChecksums(data)
	if got["argoz-amd64"] != "abc123" || got["argoz-arm64"] != "def456" {
		t.Fatalf("unexpected checksums: %#v", got)
	}
}

func TestDownloadUpdateTo(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("自更新仅支持 Linux")
	}
	asset, err := updateAssetName()
	if err != nil {
		t.Skip(err)
	}

	payload := []byte{0x7f, 'E', 'L', 'F'}
	sum := sha256.Sum256(payload)
	mux := http.NewServeMux()
	mux.HandleFunc("/repo/releases/latest/download/"+asset, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	})
	mux.HandleFunc("/repo/releases/latest/download/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), asset)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	oldBase, oldHTTP, oldMin := githubWebBase, updateHTTP, minUpdateBytes
	githubWebBase, updateHTTP, minUpdateBytes = srv.URL, srv.Client(), int64(len(payload))
	defer func() { githubWebBase, updateHTTP, minUpdateBytes = oldBase, oldHTTP, oldMin }()

	target := filepath.Join(t.TempDir(), "argoz")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := downloadUpdateTo(context.Background(), "repo", target); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("target content mismatch: %x", got)
	}
}
