// SPDX-License-Identifier: Apache-2.0

package cache

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// PkgCache manages the per-tool package caches mounted into containers
// (e.g., ~/.silo/cache/python/pip, ~/.silo/cache/rust/cargo). These grow
// unbounded with use — a heavy Rust user can easily have 10+ GB of cargo
// registry there — so size-based pruning is the common GC axis.
type PkgCache struct {
	root string // typically ~/.silo/cache
}

// NewPkgCache wraps a root directory ("" → ~/.silo/cache).
func NewPkgCache(root string) *PkgCache {
	return &PkgCache{root: root}
}

// MountSpec describes a single cache mount for the GC to evaluate.
type MountSpec struct {
	Tool         string // tool name (e.g. "python")
	Subdir       string // subdir under ~/.silo/cache/<tool> (e.g. "pip"); "" = the tool's root cache dir
	HostPath     string // resolved host path (e.g. ~/.silo/cache/python/pip)
	MaxSizeBytes uint64 // 0 = no cap
	MaxAge       time.Duration
}

// PkgGCResult summarises a per-tool cache pass.
type PkgGCResult struct {
	// Cleared is the list of "tool/subdir" entries that exceeded the cap and
	// were wiped.
	Cleared    []string
	FreedBytes uint64
}

// GC applies `specs` to the cache. For each spec, if the host path exceeds
// the size cap, the entire subdirectory is removed (next run repopulates).
// Age-based eviction is done file-by-file: files untouched beyond MaxAge
// are deleted individually, preserving still-warm data in the same mount.
func (p *PkgCache) GC(specs []MountSpec) (PkgGCResult, error) {
	var result PkgGCResult
	for _, s := range specs {
		freed, cleared := gcMount(s)
		if cleared {
			result.Cleared = append(result.Cleared, s.Tool+"/"+s.Subdir)
		}
		result.FreedBytes += freed
	}
	return result, nil
}

// gcMount applies size/age policy to a single mount. Returns (freed, fullyCleared).
func gcMount(s MountSpec) (uint64, bool) {
	info, err := os.Stat(s.HostPath)
	if err != nil || !info.IsDir() {
		return 0, false
	}

	// Age-based pruning: delete files older than cutoff.
	var freed uint64
	if s.MaxAge > 0 {
		cutoff := time.Now().Add(-s.MaxAge)
		_ = filepath.Walk(s.HostPath, func(path string, fi os.FileInfo, werr error) error {
			if werr != nil || fi.IsDir() {
				return nil //nolint:nilerr // walk errors are intentionally skipped — cache GC is best-effort
			}
			t := fileAccessTime(fi)
			if t.IsZero() || t.After(cutoff) {
				return nil
			}
			sz := fileBlocksBytes(fi)
			if err := os.Remove(path); err == nil {
				freed += sz
			}
			return nil
		})
	}

	// Size-based pruning: if still over cap, nuke the whole mount.
	if s.MaxSizeBytes > 0 {
		total := dirDiskSize(s.HostPath)
		if total > s.MaxSizeBytes {
			freed += total
			_ = os.RemoveAll(s.HostPath)
			return freed, true
		}
	}
	return freed, false
}

// DirDiskSize returns the on-disk (blocks-based) size of a directory tree.
func DirDiskSize(path string) uint64 { return dirDiskSize(path) }

func dirDiskSize(path string) uint64 {
	var total uint64
	_ = filepath.Walk(path, func(_ string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return nil //nolint:nilerr // walk errors are intentionally skipped — size probe is best-effort
		}
		total += fileBlocksBytes(fi)
		return nil
	})
	return total
}

func fileBlocksBytes(fi os.FileInfo) uint64 {
	if sys, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint64(sys.Blocks) * 512
	}
	return uint64(fi.Size())
}

func fileAccessTime(fi os.FileInfo) time.Time {
	if sys, ok := fi.Sys().(*syscall.Stat_t); ok {
		return time.Unix(sys.Atimespec.Sec, sys.Atimespec.Nsec)
	}
	return fi.ModTime()
}

// SplitMountKey parses a "tool/subdir" key as used in ToolCachePolicy.PerMount.
// Returns (tool, subdir). A bare "tool" key is (tool, "").
func SplitMountKey(key string) (string, string) {
	if i := strings.IndexByte(key, '/'); i >= 0 {
		return key[:i], key[i+1:]
	}
	return key, ""
}
