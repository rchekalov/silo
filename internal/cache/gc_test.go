// SPDX-License-Identifier: Apache-2.0

package cache

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// seedEntries populates the cache with n entries labelled "sha256:<i>" of
// `size` bytes each, and backdates LastUsed so entries[0] is oldest.
func seedEntries(t *testing.T, c *Rootfs, n int, size int) []string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "src.ext4")
	if err := os.WriteFile(src, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	digests := make([]string, n)
	for i := range n {
		d := fmt.Sprintf("sha256:%02x", i)
		digests[i] = d
		if err := c.Store(src, d, uint64(size)); err != nil {
			t.Fatal(err)
		}
		// oldest-first: entry 0 backdated most
		backdate := time.Now().Add(-time.Duration(n-i) * time.Hour)
		_ = writeTime(c.lastUsedPath(d), backdate)
	}
	return digests
}

func TestGCAgeBased(t *testing.T) {
	c := NewRootfs(t.TempDir())
	digests := seedEntries(t, c, 3, 1024)

	res, err := c.GC(GCPolicy{MaxAge: 90 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	// Entries backdated by 1h/2h/3h: 2h + 3h past the 90m cutoff, 1h inside.
	if len(res.Evicted) != 2 {
		t.Fatalf("expected 2 evictions, got %d", len(res.Evicted))
	}
	// The two oldest (digests[0], digests[1]) should be gone; digests[2] kept.
	if c.Has(digests[0], 1024) || c.Has(digests[1], 1024) {
		t.Fatal("expected oldest entries to be evicted")
	}
	if !c.Has(digests[2], 1024) {
		t.Fatal("expected newest entry to be kept")
	}
}

func TestGCSizeBased(t *testing.T) {
	c := NewRootfs(t.TempDir())
	// Use files large enough that on-disk block size doesn't dominate.
	const fileSize = 64 * 1024
	digests := seedEntries(t, c, 4, fileSize)

	// Cap total at fileSize*2.5 — two oldest entries should be evicted.
	res, err := c.GC(GCPolicy{MaxTotalBytes: fileSize*2 + fileSize/2})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Evicted) < 2 {
		t.Fatalf("expected ≥2 evictions, got %d", len(res.Evicted))
	}
	if c.Has(digests[0], fileSize) {
		t.Fatal("oldest entry must be evicted under size pressure")
	}
	if !c.Has(digests[3], fileSize) {
		t.Fatal("newest entry must be kept under size pressure")
	}
}

func TestGCNoopOnZeroPolicy(t *testing.T) {
	c := NewRootfs(t.TempDir())
	seedEntries(t, c, 2, 512)
	res, err := c.GC(GCPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Evicted) != 0 {
		t.Fatalf("zero policy must not evict, got %d", len(res.Evicted))
	}
}

func TestGCAgeThenSize(t *testing.T) {
	c := NewRootfs(t.TempDir())
	const fileSize = 64 * 1024
	digests := seedEntries(t, c, 4, fileSize)

	// 2h age cap evicts digests[0,1] (backdated 4h and 3h via seedEntries's
	// n-i formula: 4-0=4h, 4-1=3h, 4-2=2h, 4-3=1h). Then size cap at one
	// file-size evicts digests[2] too. digests[3] survives.
	res, err := c.GC(GCPolicy{
		MaxAge:        150 * time.Minute,
		MaxTotalBytes: fileSize + fileSize/2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Evicted) < 3 {
		t.Fatalf("expected ≥3 evictions, got %d", len(res.Evicted))
	}
	if !c.Has(digests[3], fileSize) {
		t.Fatal("newest entry should survive both passes")
	}
}
