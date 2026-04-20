// SPDX-License-Identifier: Apache-2.0

package cache

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCacheKeyFormat(t *testing.T) {
	c := NewRootfs("/tmp/test-cache")
	got := filepath.Base(c.Path("sha256:abc123def", 2147483648))
	want := "abc123def.ext4"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestCacheMiss(t *testing.T) {
	c := NewRootfs(t.TempDir())
	if c.Has("sha256:abc", 1024) {
		t.Fatal("expected cache miss on empty dir")
	}
}

func TestRemoveByDigest(t *testing.T) {
	c := NewRootfs(t.TempDir())
	src := filepath.Join(t.TempDir(), "src.ext4")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.Store(src, "sha256:abc", 1); err != nil {
		t.Fatal(err)
	}
	if !c.Has("sha256:abc", 1) {
		t.Fatal("expected hit after store")
	}
	if err := c.RemoveByDigest("sha256:abc", 1); err != nil {
		t.Fatal(err)
	}
	if c.Has("sha256:abc", 1) {
		t.Fatal("expected miss after removal")
	}
	if err := c.RemoveByDigest("sha256:abc", 1); err != nil {
		t.Fatalf("second remove errored: %v", err)
	}
}

func TestStoreAndCloneRoundtrip(t *testing.T) {
	c := NewRootfs(t.TempDir())
	src := filepath.Join(t.TempDir(), "src.ext4")
	if err := os.WriteFile(src, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.Store(src, "sha256:abc", 7); err != nil {
		t.Fatal(err)
	}
	if !c.Has("sha256:abc", 7) {
		t.Fatal("expected cache hit after Store")
	}
	dst := filepath.Join(t.TempDir(), "dst.ext4")
	if err := c.CloneTo(dst, "sha256:abc", 7); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "payload" {
		t.Fatalf("clone contents %q", b)
	}
}

// The real bug this fixes: storing the same digest at two different sizes
// used to produce two files. Now they collapse into one.
func TestStoreSameDigestDifferentSizes(t *testing.T) {
	c := NewRootfs(t.TempDir())

	small := filepath.Join(t.TempDir(), "small.ext4")
	if err := os.WriteFile(small, make([]byte, 1024), 0o644); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(t.TempDir(), "big.ext4")
	if err := os.WriteFile(big, make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := c.Store(small, "sha256:xyz", 1024); err != nil {
		t.Fatal(err)
	}
	if err := c.Store(big, "sha256:xyz", 4096); err != nil {
		t.Fatal(err)
	}

	files, err := c.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d: %v", len(files), files)
	}
	st, err := os.Stat(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() != 4096 {
		t.Fatalf("expected largest-kept size 4096, got %d", st.Size())
	}
}

func TestStoreKeepsLargerExisting(t *testing.T) {
	c := NewRootfs(t.TempDir())
	big := filepath.Join(t.TempDir(), "big.ext4")
	small := filepath.Join(t.TempDir(), "small.ext4")
	if err := os.WriteFile(big, make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(small, make([]byte, 1024), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.Store(big, "sha256:xyz", 4096); err != nil {
		t.Fatal(err)
	}
	if err := c.Store(small, "sha256:xyz", 1024); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(c.Path("sha256:xyz", 0))
	if st.Size() != 4096 {
		t.Fatalf("small Store must not shrink existing entry, got %d", st.Size())
	}
}

func TestHasRejectsTooSmall(t *testing.T) {
	c := NewRootfs(t.TempDir())
	src := filepath.Join(t.TempDir(), "src.ext4")
	if err := os.WriteFile(src, make([]byte, 1024), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.Store(src, "sha256:xyz", 1024); err != nil {
		t.Fatal(err)
	}
	if c.Has("sha256:xyz", 4096) {
		t.Fatal("Has should reject when stored entry is smaller than required")
	}
	if !c.Has("sha256:xyz", 1024) {
		t.Fatal("Has should accept when stored entry is ≥ required")
	}
}

func TestMigrateCollapsesLegacyEntries(t *testing.T) {
	dir := t.TempDir()
	c := NewRootfs(dir)
	// Create two legacy entries for the same digest at different sizes.
	small := filepath.Join(dir, "abcdef_1024.ext4")
	big := filepath.Join(dir, "abcdef_4096.ext4")
	if err := os.WriteFile(small, make([]byte, 1024), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(big, make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	// Also a legacy entry for a different digest.
	lone := filepath.Join(dir, "beef_2048.ext4")
	if err := os.WriteFile(lone, make([]byte, 2048), 0o644); err != nil {
		t.Fatal(err)
	}

	migrated, removed, err := c.Migrate()
	if err != nil {
		t.Fatal(err)
	}
	if migrated != 2 || removed != 1 {
		t.Fatalf("migrated=%d removed=%d, want 2 and 1", migrated, removed)
	}

	// Verify post-state.
	if _, err := os.Stat(filepath.Join(dir, "abcdef.ext4")); err != nil {
		t.Fatalf("expected abcdef.ext4: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "beef.ext4")); err != nil {
		t.Fatalf("expected beef.ext4: %v", err)
	}
	for _, p := range []string{small, big, lone} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("expected legacy %s to be gone", p)
		}
	}
	st, _ := os.Stat(filepath.Join(dir, "abcdef.ext4"))
	if st.Size() != 4096 {
		t.Fatalf("migrated entry should be the largest (4096), got %d", st.Size())
	}
}

func TestMigrateSkipsWhenModernAlreadyLarger(t *testing.T) {
	dir := t.TempDir()
	c := NewRootfs(dir)
	// A modern entry exists and is large.
	if err := os.WriteFile(filepath.Join(dir, "abcdef.ext4"), make([]byte, 8192), 0o644); err != nil {
		t.Fatal(err)
	}
	// Two smaller legacy entries.
	if err := os.WriteFile(filepath.Join(dir, "abcdef_1024.ext4"), make([]byte, 1024), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "abcdef_4096.ext4"), make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}

	migrated, removed, err := c.Migrate()
	if err != nil {
		t.Fatal(err)
	}
	if migrated != 0 || removed != 2 {
		t.Fatalf("migrated=%d removed=%d, want 0 and 2", migrated, removed)
	}
	st, _ := os.Stat(filepath.Join(dir, "abcdef.ext4"))
	if st.Size() != 8192 {
		t.Fatalf("modern entry should be preserved (8192), got %d", st.Size())
	}
}

func TestEntriesSortedOldestFirst(t *testing.T) {
	c := NewRootfs(t.TempDir())
	src := filepath.Join(t.TempDir(), "src.ext4")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, d := range []string{"sha256:aa", "sha256:bb", "sha256:cc"} {
		if err := c.Store(src, d, 1); err != nil {
			t.Fatal(err)
		}
		// Backdate lastused to differentiate.
		_ = writeTime(c.lastUsedPath(d), time.Now().Add(-time.Duration(d[len(d)-1])*time.Hour))
	}

	entries, err := c.Entries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("want 3 entries, got %d", len(entries))
	}
	for i := 1; i < len(entries); i++ {
		if entries[i].LastUsed.Before(entries[i-1].LastUsed) {
			t.Fatalf("entries not sorted oldest-first: %+v", entries)
		}
	}
}
