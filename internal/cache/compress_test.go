// SPDX-License-Identifier: Apache-2.0

package cache

import (
	"bytes"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
)

func TestCompressDecompressRoundtrip(t *testing.T) {
	// Real ext4 data is largely compressible; use random bytes for a worst-case
	// check that the codec is lossless.
	payload := make([]byte, 128*1024)
	_, _ = rand.Read(payload)

	src := filepath.Join(t.TempDir(), "src.ext4")
	if err := os.WriteFile(src, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	comp := filepath.Join(t.TempDir(), "src.ext4.zst")
	if err := compressExt4(src, comp); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "dst.ext4")
	if err := decompressExt4(comp, dst); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("round-trip mismatch")
	}
}

func TestRootfsCompressAndDecompress(t *testing.T) {
	c := NewRootfs(t.TempDir())
	// Repetitive payload → compresses well, test hot→cold savings.
	payload := bytes.Repeat([]byte("silo-payload-"), 4096)
	src := filepath.Join(t.TempDir(), "src.ext4")
	if err := os.WriteFile(src, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.Store(src, "sha256:abc", uint64(len(payload))); err != nil {
		t.Fatal(err)
	}

	if err := c.Compress("sha256:abc"); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(c.Path("sha256:abc", 0)); !os.IsNotExist(err) {
		t.Fatal("raw entry should be gone after Compress")
	}
	if _, err := os.Stat(c.compressedPath("sha256:abc")); err != nil {
		t.Fatalf("compressed entry missing: %v", err)
	}
	// Has should still succeed via the cold path.
	if !c.Has("sha256:abc", uint64(len(payload))) {
		t.Fatal("Has() should accept compressed entries")
	}

	dst := filepath.Join(t.TempDir(), "dst.ext4")
	if err := c.CloneTo(dst, "sha256:abc", uint64(len(payload))); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dst)
	if !bytes.Equal(got, payload) {
		t.Fatal("cloned bytes don't match original after cold-path hit")
	}
	// After CloneTo, the hot copy should exist again (promoted).
	if _, err := os.Stat(c.Path("sha256:abc", 0)); err != nil {
		t.Fatalf("CloneTo should promote cold entry to hot, got: %v", err)
	}
}

func TestDecompressMissing(t *testing.T) {
	c := NewRootfs(t.TempDir())
	err := c.Decompress("sha256:nope")
	if err == nil {
		t.Fatal("Decompress on absent digest should fail")
	}
}
