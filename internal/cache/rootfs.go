// SPDX-License-Identifier: Apache-2.0

// Package cache manages cached unpacked rootfs ext4 images keyed by image
// digest. On APFS (the default on Apple Silicon macOS installs) Clonefile
// produces instant copy-on-write clones so we skip the ~25 s OCI unpack on
// cache hit.
//
// Cache layout:
//   ~/.silo/rootfs-cache/<digest-hex>.ext4       — the cached rootfs
//   ~/.silo/rootfs-cache/<digest-hex>.lastused   — last CloneTo timestamp (LRU)
//
// Historically entries were keyed on (digest, size) which stored the same
// content twice when a tool's `rootfsSizeMB` changed. Migrate() collapses any
// legacy `<digest>_<bytes>.ext4` files to the digest-only form, keeping the
// largest size seen (ext4 files are sparse — oversizing is ~free).
package cache

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/rchekalov/silo/internal/runtime"
	"golang.org/x/sys/unix"
)

// Rootfs is a digest-keyed cache of rootfs ext4 files under ~/.silo/rootfs-cache/.
type Rootfs struct {
	dir string
}

// NewRootfs returns a Rootfs cache under ~/.silo/rootfs-cache (override via dir).
func NewRootfs(dir string) *Rootfs {
	if dir == "" {
		dir = runtime.RootfsCache()
	}
	return &Rootfs{dir: dir}
}

// Dir returns the cache directory.
func (c *Rootfs) Dir() string { return c.dir }

// Path returns the cache filepath for a digest. The second argument is kept
// for API compatibility with callers that pass a size hint; it does not affect
// the file location.
func (c *Rootfs) Path(digest string, _ uint64) string {
	return filepath.Join(c.dir, cacheKey(digest)+".ext4")
}

// Has reports whether a cache entry exists that is at least `minSizeBytes`
// large (apparent/sparse size). A smaller existing entry is treated as a miss
// so the caller re-unpacks at the larger size; `Store` will then overwrite.
//
// Accepts both the hot form (`<digest>.ext4`) and the cold form
// (`<digest>.ext4.zst`). For the compressed form we can't cheaply know the
// uncompressed size without decompressing, so we assume it matches the size
// at which it was originally stored — callers that need a specific larger
// size must trigger a re-pull, which will restore a raw entry.
func (c *Rootfs) Has(digest string, minSizeBytes uint64) bool {
	if st, err := os.Stat(c.Path(digest, 0)); err == nil {
		return uint64(st.Size()) >= minSizeBytes
	}
	if _, err := os.Stat(c.compressedPath(digest)); err == nil {
		// Assume compressed entry satisfies the size — the safer default is
		// true so we don't waste a re-pull. If the decompressed ext4 ends up
		// too small for the caller's needs the clone step will still succeed
		// (ext4 mounts at its own recorded size).
		return true
	}
	return false
}

// CloneTo materialises a cache entry at `destination` using APFS clonefile
// (instant, COW). Falls back to regular copy on non-APFS filesystems. Also
// touches the lastused marker so GC can evict by LRU.
//
// Handles both hot (raw ext4) and cold (zstd) forms. A cold hit is promoted
// back to hot after decompression so the next run gets the fast clonefile
// path again.
func (c *Rootfs) CloneTo(destination, digest string, minSizeBytes uint64) error {
	src := c.Path(digest, 0)
	_ = os.Remove(destination)

	if _, err := os.Stat(src); err != nil {
		// Raw cache file missing — try the compressed form and promote to hot.
		cp := c.compressedPath(digest)
		if _, cerr := os.Stat(cp); cerr != nil {
			return err
		}
		if derr := decompressExt4(cp, src); derr != nil {
			return derr
		}
		// Best-effort: keep both hot and cold present so subsequent GC can
		// decide to re-evict the raw copy. Drop the cold copy only if the
		// caller later calls Compress() again.
	}

	if err := unix.Clonefile(src, destination, 0); err != nil {
		if ferr := copyFile(src, destination); ferr != nil {
			return ferr
		}
	}
	c.touch(digest)
	return nil
}

// Store atomically installs `source` as the cache entry for `digest`. If an
// entry already exists at an equal-or-larger apparent size, Store is a no-op
// (prefer the larger copy). Otherwise the source replaces the existing entry.
func (c *Rootfs) Store(source, digest string, sizeBytes uint64) error {
	dest := c.Path(digest, 0)

	srcInfo, err := os.Stat(source)
	if err != nil {
		return err
	}
	// sizeBytes is the caller's intent (the requested ext4 size). Prefer the
	// actual source file size so "size" always reflects what's on disk.
	storeSize := max(uint64(srcInfo.Size()), sizeBytes)

	if existing, err := os.Stat(dest); err == nil {
		if uint64(existing.Size()) >= storeSize {
			return nil
		}
	}

	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(c.dir, "tmp-"+randomHex()+".ext4")
	if err := copyFile(source, tmp); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	c.touch(digest)
	return nil
}

// List returns every cache entry (*.ext4 and *.ext4.zst, excluding tmp-*),
// sorted. Each returned path is a full filepath under c.dir.
func (c *Rootfs) List() ([]string, error) {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "tmp-") {
			continue
		}
		if strings.HasSuffix(name, ".ext4") || strings.HasSuffix(name, CompressedSuffix) {
			out = append(out, filepath.Join(c.dir, name))
		}
	}
	return out, nil
}

// RemoveByDigest deletes the cache entry for `digest` (raw + compressed +
// sidecar). No-op if absent. The size argument is ignored (kept for API
// compatibility).
func (c *Rootfs) RemoveByDigest(digest string, _ uint64) error {
	for _, path := range []string{c.Path(digest, 0), c.compressedPath(digest), c.lastUsedPath(digest)} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// SizeOfEntry returns the apparent on-disk size of the entry, or 0 if absent.
// The size argument is ignored.
func (c *Rootfs) SizeOfEntry(digest string, _ uint64) uint64 {
	st, err := os.Stat(c.Path(digest, 0))
	if err != nil {
		return 0
	}
	return uint64(st.Size())
}

// SizeOfCompressed returns the on-disk size of the compressed form, or 0 if
// not present.
func (c *Rootfs) SizeOfCompressed(digest string) uint64 {
	return fileDiskSize(c.compressedPath(digest))
}

// Clear removes every cached ext4 file (raw + compressed) and lastused sidecar.
func (c *Rootfs) Clear() error {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".ext4") ||
			strings.HasSuffix(name, CompressedSuffix) ||
			strings.HasSuffix(name, ".lastused") {
			if err := os.Remove(filepath.Join(c.dir, name)); err != nil {
				return err
			}
		}
	}
	return nil
}

// TotalSize sums the apparent byte sizes of every cache entry.
func (c *Rootfs) TotalSize() (uint64, error) {
	entries, err := c.List()
	if err != nil {
		return 0, err
	}
	var total uint64
	for _, p := range entries {
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		total += uint64(st.Size())
	}
	return total, nil
}

// TotalDiskSize sums the actual on-disk (blocks * 512) size of every entry.
// More accurate than TotalSize for sparse ext4 files.
func (c *Rootfs) TotalDiskSize() (uint64, error) {
	entries, err := c.List()
	if err != nil {
		return 0, err
	}
	var total uint64
	for _, p := range entries {
		total += fileDiskSize(p)
	}
	return total, nil
}

// LastUsed returns the recorded lastused time for a digest, or the file's
// mtime if the sidecar is missing (so newly-stored entries aren't immediately
// picked as the LRU victim).
func (c *Rootfs) LastUsed(digest string) time.Time {
	if t, err := readTime(c.lastUsedPath(digest)); err == nil {
		return t
	}
	if st, err := os.Stat(c.Path(digest, 0)); err == nil {
		return st.ModTime()
	}
	return time.Time{}
}

// Entries returns one entry per digest present in the cache, whether it's
// hot (raw), cold (compressed), or both. Sorted by lastused ascending
// (oldest first) for LRU eviction convenience.
func (c *Rootfs) Entries() ([]Entry, error) {
	paths, err := c.List()
	if err != nil {
		return nil, err
	}
	// Collapse by digest so a hot+cold pair is one entry with summed disk size.
	byDigest := map[string]*Entry{}
	for _, p := range paths {
		name := filepath.Base(p)
		compressed := strings.HasSuffix(name, CompressedSuffix)
		var stem string
		if compressed {
			stem = strings.TrimSuffix(name, CompressedSuffix)
		} else {
			stem = strings.TrimSuffix(name, ".ext4")
		}
		// Skip legacy `<hex>_<bytes>.ext4` — Migrate() should have cleaned them up.
		if strings.Contains(stem, "_") {
			continue
		}
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		digest := "sha256:" + stem
		e, ok := byDigest[digest]
		if !ok {
			e = &Entry{Digest: digest, LastUsed: c.LastUsed(digest)}
			byDigest[digest] = e
		}
		if compressed {
			e.CompressedPath = p
			e.CompressedSize = fileDiskSize(p)
		} else {
			e.Path = p
			e.ApparentSize = uint64(st.Size())
			e.DiskSize = fileDiskSize(p)
		}
	}

	out := make([]Entry, 0, len(byDigest))
	for _, e := range byDigest {
		out = append(out, *e)
	}
	sortEntriesByLastUsedAsc(out)
	return out, nil
}

// Entry describes one cached rootfs for enumeration / GC. Path/DiskSize
// describe the raw ext4 form (when present). CompressedPath/CompressedSize
// describe the zstd form.
type Entry struct {
	Digest         string
	Path           string
	ApparentSize   uint64
	DiskSize       uint64
	CompressedPath string
	CompressedSize uint64
	LastUsed       time.Time
}

// Compressed reports whether this entry has a cold (zstd) form.
func (e Entry) Compressed() bool { return e.CompressedPath != "" }

// Raw reports whether this entry has a hot (plain ext4) form.
func (e Entry) Raw() bool { return e.Path != "" }

// EffectiveDiskSize returns the sum of hot+cold disk footprint.
func (e Entry) EffectiveDiskSize() uint64 { return e.DiskSize + e.CompressedSize }

// Migrate collapses any legacy `<digest>_<bytes>.ext4` files into the new
// digest-only form. When multiple sizes exist for the same digest, the
// largest is kept and renamed; the others are deleted. Returns (migrated,
// removed) counts.
func (c *Rootfs) Migrate() (migrated, removed int, err error) {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, err
	}

	type candidate struct {
		path string
		size int64
	}
	groups := map[string][]candidate{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".ext4") || strings.HasPrefix(name, "tmp-") {
			continue
		}
		stem := strings.TrimSuffix(name, ".ext4")
		// New form is just `<hex>`; legacy is `<hex>_<bytes>`.
		if !strings.Contains(stem, "_") {
			continue
		}
		hex, _, ok := splitLastUnderscore(stem)
		if !ok {
			continue
		}
		full := filepath.Join(c.dir, name)
		st, serr := os.Stat(full)
		if serr != nil {
			continue
		}
		groups[hex] = append(groups[hex], candidate{path: full, size: st.Size()})
	}

	for digestHex, cands := range groups {
		// Pick the largest.
		var keep candidate
		for _, cand := range cands {
			if cand.size > keep.size {
				keep = cand
			}
		}
		target := filepath.Join(c.dir, digestHex+".ext4")

		// If a modern entry already exists and is at least as big, just
		// remove all legacy ones.
		if existing, serr := os.Stat(target); serr == nil && existing.Size() >= keep.size {
			for _, cand := range cands {
				if err := os.Remove(cand.path); err == nil {
					removed++
				}
			}
			continue
		}

		// Rename the winner, remove the rest.
		if err := os.Rename(keep.path, target); err != nil {
			return migrated, removed, fmt.Errorf("migrate %s: %w", keep.path, err)
		}
		migrated++
		for _, cand := range cands {
			if cand.path == keep.path {
				continue
			}
			if err := os.Remove(cand.path); err == nil {
				removed++
			}
		}
	}
	return migrated, removed, nil
}

// touch writes the current time to the digest's lastused sidecar.
func (c *Rootfs) touch(digest string) {
	_ = os.MkdirAll(c.dir, 0o755)
	_ = writeTime(c.lastUsedPath(digest), time.Now())
}

func (c *Rootfs) lastUsedPath(digest string) string {
	return filepath.Join(c.dir, cacheKey(digest)+".lastused")
}

// compressedPath returns the path where a zstd-compressed form lives.
func (c *Rootfs) compressedPath(digest string) string {
	return filepath.Join(c.dir, cacheKey(digest)+CompressedSuffix)
}

// Compress converts the raw ext4 entry for `digest` into zstd form, removing
// the raw file on success. Safe to call if already compressed (no-op).
func (c *Rootfs) Compress(digest string) error {
	raw := c.Path(digest, 0)
	if _, err := os.Stat(raw); err != nil {
		return err
	}
	comp := c.compressedPath(digest)
	if err := compressExt4(raw, comp); err != nil {
		return err
	}
	return os.Remove(raw)
}

// Decompress converts the compressed entry for `digest` back into raw form,
// keeping the compressed file so subsequent clones still have the hot path
// if callers don't remove it. Returns errNotCompressed if there is nothing
// to decompress.
func (c *Rootfs) Decompress(digest string) error {
	comp := c.compressedPath(digest)
	if _, err := os.Stat(comp); err != nil {
		if os.IsNotExist(err) {
			return errNotCompressed
		}
		return err
	}
	return decompressExt4(comp, c.Path(digest, 0))
}

// cacheKey returns the hex digest with any "sha256:" prefix stripped.
func cacheKey(digest string) string {
	return strings.TrimPrefix(digest, "sha256:")
}

// splitLastUnderscore splits "<hex>_<bytes>" into (hex, bytes-string, ok).
func splitLastUnderscore(s string) (string, string, bool) {
	i := strings.LastIndex(s, "_")
	if i < 0 {
		return "", "", false
	}
	return s[:i], s[i+1:], true
}

// fileDiskSize returns the on-disk size of a file (stat.Blocks * 512). 0 if
// stat fails or the FS doesn't report blocks. os.FileInfo.Sys() returns a
// *syscall.Stat_t on all Unixes; sys/unix.Stat_t is a different type even
// though the layout matches.
func fileDiskSize(path string) uint64 {
	st, err := os.Stat(path)
	if err != nil {
		return 0
	}
	if sys, ok := st.Sys().(*syscall.Stat_t); ok {
		return uint64(sys.Blocks) * 512
	}
	return uint64(st.Size())
}

// randomHex generates 16 hex chars for temp file names.
func randomHex() string {
	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

// readTime reads an RFC3339Nano timestamp from path.
func readTime(path string) (time.Time, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339Nano, strings.TrimSpace(string(raw)))
}

// writeTime writes an RFC3339Nano timestamp atomically.
func writeTime(path string, t time.Time) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(t.UTC().Format(time.RFC3339Nano)), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// sortEntriesByLastUsedAsc sorts oldest-first (zero-time first).
func sortEntriesByLastUsedAsc(e []Entry) {
	// Small n (usually <30), insertion sort is fine and keeps zero-value deps.
	for i := 1; i < len(e); i++ {
		j := i
		for j > 0 && e[j].LastUsed.Before(e[j-1].LastUsed) {
			e[j], e[j-1] = e[j-1], e[j]
			j--
		}
	}
}
