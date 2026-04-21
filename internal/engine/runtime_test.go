// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rchekalov/silo/internal/runtime"
)

// fakeBundle builds an in-memory silo-runtime-arm64.tar.gz with the two files
// the production client expects.
func fakeBundle(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct{ name, body string }{
		{"vmlinux", "fake-kernel"},
		{"initfs.ext4", "fake-initfs"},
	} {
		hdr := &tar.Header{
			Name: f.name,
			Mode: 0o644,
			Size: int64(len(f.body)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(f.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// bundleServer mirrors the real GitHub release layout so tests exercise the
// real URL construction. The `path` arg is the asset name — `serveSum`
// controls whether the .sha256 sidecar matches the bundle.
func bundleServer(t *testing.T, bundle []byte, advertisedSum string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/"+runtimeBundleAsset):
			_, _ = w.Write(bundle)
		case strings.HasSuffix(r.URL.Path, "/"+runtimeBundleChecksum):
			fmt.Fprintf(w, "%s  %s\n", advertisedSum, runtimeBundleAsset)
		default:
			http.NotFound(w, r)
		}
	}))
}

// withSiloHome points ~/.silo at a fresh temp dir so runtime.Kernel() etc.
// don't collide with the real install.
func withSiloHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := runtime.EnsureDirectories(); err != nil {
		t.Fatal(err)
	}
	return home
}

// stubBundleURL redirects the prod URL builder at a test server for the
// duration of the test.
func stubBundleURL(t *testing.T, base string) {
	t.Helper()
	prev := runtimeBundleBaseURL
	runtimeBundleBaseURL = func() string { return base }
	t.Cleanup(func() { runtimeBundleBaseURL = prev })
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestTryDownloadRuntimeBundle_Success(t *testing.T) {
	withSiloHome(t)
	bundle := fakeBundle(t)
	srv := bundleServer(t, bundle, sha256Hex(bundle))
	defer srv.Close()
	stubBundleURL(t, srv.URL)

	ok, err := tryDownloadRuntimeBundle()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected download to succeed")
	}

	for _, want := range []struct{ path, body string }{
		{runtime.Kernel(), "fake-kernel"},
		{runtime.Initfs(), "fake-initfs"},
	} {
		got, err := os.ReadFile(want.path)
		if err != nil {
			t.Fatalf("read %s: %v", want.path, err)
		}
		if string(got) != want.body {
			t.Fatalf("%s: want %q, got %q", want.path, want.body, got)
		}
	}

	// Scratch files under ~/.silo/.local should be cleaned up after success.
	for _, scratch := range []string{runtimeBundleAsset, runtimeBundleChecksum} {
		p := filepath.Join(runtime.LocalDownloads(), scratch)
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("expected %s to be removed after success, stat err=%v", p, err)
		}
	}
}

func TestTryDownloadRuntimeBundle_ChecksumMismatch(t *testing.T) {
	withSiloHome(t)
	bundle := fakeBundle(t)
	srv := bundleServer(t, bundle, strings.Repeat("0", 64)) // deliberate mismatch
	defer srv.Close()
	stubBundleURL(t, srv.URL)

	ok, err := tryDownloadRuntimeBundle()
	if err != nil {
		t.Fatalf("checksum mismatch should not be a hard error, got %v", err)
	}
	if ok {
		t.Fatal("checksum mismatch should report miss (false)")
	}

	if _, err := os.Stat(runtime.Kernel()); !os.IsNotExist(err) {
		t.Fatalf("kernel must not be installed on checksum mismatch (stat err=%v)", err)
	}
	if _, err := os.Stat(runtime.Initfs()); !os.IsNotExist(err) {
		t.Fatalf("initfs must not be installed on checksum mismatch (stat err=%v)", err)
	}
}

func TestTryDownloadRuntimeBundle_MissingChecksum(t *testing.T) {
	withSiloHome(t)
	// Server that 404s for everything.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()
	stubBundleURL(t, srv.URL)

	ok, err := tryDownloadRuntimeBundle()
	if err != nil {
		t.Fatalf("missing asset should not be a hard error, got %v", err)
	}
	if ok {
		t.Fatal("missing asset should report miss (false)")
	}
}

func TestReadExpectedSha256(t *testing.T) {
	dir := t.TempDir()
	sumPath := filepath.Join(dir, "x.sha256")
	hex := strings.Repeat("ab", 32) // 64 hex chars
	if err := os.WriteFile(sumPath, []byte(hex+"  bundle.tar.gz\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readExpectedSha256(sumPath)
	if err != nil {
		t.Fatal(err)
	}
	if got != hex {
		t.Fatalf("want %q, got %q", hex, got)
	}

	// Invalid content should error so the caller can fall back.
	bad := filepath.Join(dir, "bad.sha256")
	if err := os.WriteFile(bad, []byte("not a sha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readExpectedSha256(bad); err == nil {
		t.Fatal("expected error for non-hex checksum content")
	}
}
