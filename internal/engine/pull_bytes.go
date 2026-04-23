// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// duBytes returns the total on-disk bytes of everything under root, using
// Stat_t.Blocks for sparse-file accuracy (the container rootfs.ext4 is
// created as a 2 GiB sparse file, so FileInfo.Size() lies about disk use).
// Returns 0 if root is missing; errors mid-walk are silently skipped.
func duBytes(root string) int64 {
	if root == "" {
		return 0
	}
	var total int64
	_ = filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil //nolint:nilerr // walk errors are intentionally skipped — duBytes is best-effort
		}
		if d.IsDir() {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil //nolint:nilerr // d.Info errors are skipped — duBytes is best-effort
		}
		if sys, ok := info.Sys().(*syscall.Stat_t); ok {
			total += sys.Blocks * 512
			return nil
		}
		total += info.Size()
		return nil
	})
	return total
}

// humanBytes formats n as a human-readable byte count.
func humanBytes(n int64) string {
	const (
		kib = 1024
		mib = kib * 1024
		gib = mib * 1024
	)
	switch {
	case n >= gib:
		return fmt.Sprintf("%.1f GiB", float64(n)/float64(gib))
	case n >= mib:
		return fmt.Sprintf("%.0f MiB", float64(n)/float64(mib))
	case n >= kib:
		return fmt.Sprintf("%.0f KiB", float64(n)/float64(kib))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
