// SPDX-License-Identifier: Apache-2.0

package cache

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPkgCacheSizeBased(t *testing.T) {
	dir := t.TempDir()
	// Create two 1KB files in a mount.
	for _, n := range []string{"a", "b"} {
		if err := os.WriteFile(filepath.Join(dir, n), make([]byte, 1024), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	pc := NewPkgCache(dir)
	res, err := pc.GC([]MountSpec{{
		Tool:         "python",
		Subdir:       "pip",
		HostPath:     dir,
		MaxSizeBytes: 1024, // 1KB cap, but 2KB present
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Cleared) != 1 {
		t.Fatalf("expected 1 mount cleared, got %v", res.Cleared)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("expected mount dir to be removed: %v", err)
	}
}

func TestPkgCacheAgeBasedPrunesOldFilesOnly(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "stale")
	fresh := filepath.Join(dir, "fresh")
	if err := os.WriteFile(old, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fresh, []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	backdated := time.Now().Add(-60 * 24 * time.Hour)
	if err := os.Chtimes(old, backdated, backdated); err != nil {
		t.Fatal(err)
	}

	pc := NewPkgCache(dir)
	_, err := pc.GC([]MountSpec{{
		Tool:     "python",
		Subdir:   "pip",
		HostPath: dir,
		MaxAge:   30 * 24 * time.Hour,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("old file should be pruned: %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("fresh file should survive: %v", err)
	}
}

func TestPkgCacheAgeThenSize(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"a", "b", "c"} {
		p := filepath.Join(dir, n)
		if err := os.WriteFile(p, make([]byte, 1024), 0o644); err != nil {
			t.Fatal(err)
		}
		if n == "a" {
			backdated := time.Now().Add(-60 * 24 * time.Hour)
			_ = os.Chtimes(p, backdated, backdated)
		}
	}

	pc := NewPkgCache(dir)
	_, err := pc.GC([]MountSpec{{
		Tool:         "python",
		Subdir:       "pip",
		HostPath:     dir,
		MaxAge:       30 * 24 * time.Hour,
		MaxSizeBytes: 1500, // 2KB remain after age prune, still over cap → whole dir nuked
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("expected mount to be fully cleared: %v", err)
	}
}

func TestSplitMountKey(t *testing.T) {
	tests := []struct{ in, tool, sub string }{
		{"python/pip", "python", "pip"},
		{"python", "python", ""},
		{"rust/cargo/registry", "rust", "cargo/registry"},
	}
	for _, tc := range tests {
		got1, got2 := SplitMountKey(tc.in)
		if got1 != tc.tool || got2 != tc.sub {
			t.Errorf("SplitMountKey(%q) = (%q, %q), want (%q, %q)", tc.in, got1, got2, tc.tool, tc.sub)
		}
	}
}
