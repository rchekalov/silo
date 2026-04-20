// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rchekalov/silo/internal/cache"
	"github.com/rchekalov/silo/internal/config"
	"github.com/rchekalov/silo/internal/runtime"
)

// staleContainerAge is the cutoff for reaping leaked ~/.silo/containers/silo-*
// directories. A container should create, run, and be deleted well inside this
// window, so anything older is almost certainly from a crashed or SIGKILL'd run.
const staleContainerAge = 30 * time.Minute

var (
	reapOnce    sync.Once
	migrateOnce sync.Once
	autoGCOnce  sync.Once
)

// maintenanceBeforeRun runs the cheap housekeeping that should happen at most
// once per process: one-off migration of legacy cache entries, a sweep for
// orphan container directories, and a passive rootfs cache GC if over cap.
// Called at the top of every run so users passively reclaim disk just by
// using silo.
func maintenanceBeforeRun() {
	migrateOnce.Do(func() {
		_, _, _ = cache.NewRootfs("").Migrate()
	})
	reapOnce.Do(func() {
		_ = reapStaleContainers(runtime.Containers(), staleContainerAge)
	})
	autoGCOnce.Do(func() {
		merged, _, err := config.FindMergedProjectConfig("")
		if err != nil {
			// Auto-GC is best-effort background work on the hot path; a broken
			// .siloconf shouldn't block the actual run. Log and keep going —
			// the command layer will surface the same error properly.
			fmt.Fprintf(os.Stderr, "silo: skipping auto-GC (config load failed: %v)\n", err)
			return
		}
		policy := effectiveRootfsGCPolicy(merged)
		if policy.MaxTotalBytes == 0 && policy.MaxAge == 0 {
			return
		}
		res, ran, err := cache.NewRootfs("").AutoGC(policy)
		if err == nil && ran && len(res.Evicted) > 0 {
			fmt.Fprintf(os.Stderr, "silo: auto-evicted %d rootfs cache entries (%.0f MiB freed)\n",
				len(res.Evicted), float64(res.FreedBytes)/1024/1024)
		}
	})
}

// effectiveRootfsGCPolicy returns the rootfs cache GC policy from a merged
// siloconf, falling back to built-in defaults.
func effectiveRootfsGCPolicy(merged *config.ProjectConfig) cache.GCPolicy {
	var cc *config.CacheConfig
	if merged != nil {
		cc = merged.Cache
	}
	rp := cc.EffectiveRootfsPolicy()
	return cache.GCPolicy{
		MaxTotalBytes: rp.MaxSizeBytes(),
		MaxAge:        rp.MaxAge(),
	}
}

// reapStaleContainers removes `silo-*` subdirectories under `dir` whose mtime
// is older than `maxAge`. Returns the first error encountered, but always
// attempts every candidate.
func reapStaleContainers(dir string, maxAge time.Duration) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	cutoff := time.Now().Add(-maxAge)
	var firstErr error
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "silo-") {
			continue
		}
		full := filepath.Join(dir, e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(cutoff) {
			continue
		}
		if err := os.RemoveAll(full); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
