// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rchekalov/silo/internal/bridge"
	"github.com/rchekalov/silo/internal/cache"
	"github.com/rchekalov/silo/internal/config"
	"github.com/rchekalov/silo/internal/errs"
	"github.com/rchekalov/silo/internal/runtime"
	"github.com/spf13/cobra"
)

var cacheCmd = &cobra.Command{
	Use:   "cache",
	Short: "Inspect and manage Silo caches",
}

var cacheReportCmd = &cobra.Command{
	Use:   "report",
	Short: "Summarise ~/.silo disk usage (rootfs cache, per-tool, images, containers)",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("~/.silo disk usage (on-disk / apparent):")

		rootfs := cache.NewRootfs("")
		_, _, _ = rootfs.Migrate()
		apparent, _ := rootfs.TotalSize()
		disk, _ := rootfs.TotalDiskSize()
		entries, _ := rootfs.Entries()
		nHot, nCold, nBoth := 0, 0, 0
		for _, e := range entries {
			switch {
			case e.Raw() && e.Compressed():
				nBoth++
			case e.Compressed():
				nCold++
			case e.Raw():
				nHot++
			}
		}
		fmt.Printf("  rootfs cache      %8.1f MiB / %8.1f MiB  (%d raw, %d zstd, %d both)\n",
			float64(disk)/1024/1024, float64(apparent)/1024/1024, nHot, nCold, nBoth)

		cacheRoot := runtime.Cache()
		var toolCacheTotal uint64
		if dirs, err := os.ReadDir(cacheRoot); err == nil {
			for _, d := range dirs {
				if d.IsDir() {
					toolCacheTotal += cache.DirDiskSize(filepath.Join(cacheRoot, d.Name()))
				}
			}
		}
		fmt.Printf("  per-tool caches   %8.1f MiB\n", float64(toolCacheTotal)/1024/1024)

		containersTotal := cache.DirDiskSize(runtime.Containers())
		fmt.Printf("  containers (live) %8.1f MiB\n", float64(containersTotal)/1024/1024)

		imagesTotal := cache.DirDiskSize(runtime.ImageStore())
		fmt.Printf("  OCI image store   %8.1f MiB\n", float64(imagesTotal)/1024/1024)

		mgr, err := bridge.NewManager(runtime.Kernel(), runtime.Initfs(), runtime.Root(), false)
		if err == nil {
			defer mgr.Close()
			if size, err := mgr.ImageStoreOrphansSize(); err == nil {
				fmt.Printf("      orphan blobs: %.1f MiB  (run `silo cache gc --images` to free)\n", float64(size)/1024/1024)
			}
		}

		buildsTotal := cache.DirDiskSize(runtime.Builds())
		fmt.Printf("  builds            %8.1f MiB\n", float64(buildsTotal)/1024/1024)

		fmt.Printf("  --\n  total             %8.1f MiB\n",
			float64(disk+toolCacheTotal+containersTotal+imagesTotal+buildsTotal)/1024/1024)
		return nil
	},
}

var cacheListCmd = &cobra.Command{
	Use:   "list",
	Short: "Show cache sizes (apparent + on-disk, rootfs + per-tool)",
	RunE: func(cmd *cobra.Command, args []string) error {
		c := cache.NewRootfs("")
		_, _, _ = c.Migrate()
		entries, _ := c.Entries()
		apparent, _ := c.TotalSize()
		disk, _ := c.TotalDiskSize()
		fmt.Printf("Rootfs cache: %d entries, %.1f MiB apparent / %.1f MiB on disk\n",
			len(entries), float64(apparent)/1024/1024, float64(disk)/1024/1024)
		for _, e := range entries {
			tier := "raw"
			if e.Compressed() && !e.Raw() {
				tier = "zstd"
			} else if e.Compressed() && e.Raw() {
				tier = "raw+zstd"
			}
			fmt.Printf("  %s  %.1f MiB disk  [%s]  (last used %s)\n",
				digestShort(e.Digest),
				float64(e.EffectiveDiskSize())/1024/1024,
				tier,
				relTime(e.LastUsed))
		}

		cacheRoot := runtime.Cache()
		if dirs, err := os.ReadDir(cacheRoot); err == nil && len(dirs) > 0 {
			fmt.Println("\nPer-tool caches:")
			var total uint64
			for _, d := range dirs {
				if !d.IsDir() {
					continue
				}
				size := cache.DirDiskSize(filepath.Join(cacheRoot, d.Name()))
				total += size
				fmt.Printf("  %-20s %.1f MiB on disk\n", d.Name(), float64(size)/1024/1024)
			}
			fmt.Printf("  %-20s %.1f MiB\n", "total", float64(total)/1024/1024)
		}
		return nil
	},
}

var (
	cacheCleanAll        bool
	cacheCleanRootfs     bool
	cacheCleanContainers bool
	cacheCleanSafe       bool
	cacheCleanDryRun     bool
	cacheCleanForce      bool
)

var (
	gcToolCaches bool
	gcImages     bool
	gcDryRun     bool
	gcMaxSizeMB  uint64
	gcMaxAgeDays uint64
)

var (
	compressOlderThan uint64
	compressAll       bool
	compressDryRun    bool
)

var cacheCompressCmd = &cobra.Command{
	Use:   "compress",
	Short: "Convert cold rootfs cache entries to zstd (trades CPU on next cold-start for disk)",
	Long: `Compresses raw rootfs cache entries using zstd. A compressed entry
takes 2.5–4× less disk but costs a one-time decompression step on the next
'silo run' of that tool (typically 1–3 s for a 500 MB image).

By default, compresses entries untouched for the last 14 days. Use --all to
compress every entry, or --older-than N to set a custom cut-off.`,
	RunE: runCacheCompress,
}

func init() {
	cacheCompressCmd.Flags().Uint64Var(&compressOlderThan, "older-than", 14, "compress entries last used more than N days ago")
	cacheCompressCmd.Flags().BoolVar(&compressAll, "all", false, "compress every raw entry regardless of age")
	cacheCompressCmd.Flags().BoolVar(&compressDryRun, "dry-run", false, "print the plan without compressing")
	cacheCmd.AddCommand(cacheCompressCmd)
}

var cacheGCCmd = &cobra.Command{
	Use:   "gc",
	Short: "Evict cold cache entries to keep disk usage bounded",
	Long: `Applies the cache policy from .siloconf (or the global ~/.silo/siloconf)
to trim the rootfs cache: oldest-first eviction until the total is under the
configured size, plus age-based eviction for entries untouched past the age
cut-off. Flags override the policy for one run.

With --tool-caches, also trims per-tool package caches (pip, npm, cargo, etc.)
under ~/.silo/cache/<tool>/...`,
	RunE: runCacheGC,
}

func init() {
	cacheGCCmd.Flags().BoolVar(&gcToolCaches, "tool-caches", false, "also GC per-tool package caches")
	cacheGCCmd.Flags().BoolVar(&gcImages, "images", false, "also GC unreferenced OCI image layers in ~/.silo/images")
	cacheGCCmd.Flags().BoolVar(&gcDryRun, "dry-run", false, "print the plan without evicting")
	cacheGCCmd.Flags().Uint64Var(&gcMaxSizeMB, "max-size", 0, "override policy: max rootfs cache size in MiB")
	cacheGCCmd.Flags().Uint64Var(&gcMaxAgeDays, "max-age", 0, "override policy: max rootfs entry age in days")
	cacheCmd.AddCommand(cacheGCCmd)
}

var cacheCleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Remove cached data",
	Long: `Without --safe: wipe all rootfs cache and/or container state (subject to
--rootfs / --containers narrowing). Use when you want a full reset.

With --safe: only remove artifacts NOT referenced by any installed tool —
orphan rootfs cache entries, orphan per-tool package caches, orphan build
rootfs, and always-stale container dirs. Installed tools are untouched.`,
	RunE: runCacheClean,
}

func init() {
	cacheCleanCmd.Flags().BoolVar(&cacheCleanAll, "all", false, "clean all caches")
	cacheCleanCmd.Flags().BoolVar(&cacheCleanRootfs, "rootfs", false, "only clean rootfs cache")
	cacheCleanCmd.Flags().BoolVar(&cacheCleanContainers, "containers", false, "only clean container state")
	cacheCleanCmd.Flags().BoolVar(&cacheCleanSafe, "safe", false, "only remove artifacts not referenced by any installed tool")
	cacheCleanCmd.Flags().BoolVar(&cacheCleanDryRun, "dry-run", false, "print the plan without acting")
	cacheCleanCmd.Flags().BoolVar(&cacheCleanForce, "force", false, "skip confirmation prompt")
	cacheCmd.AddCommand(cacheListCmd, cacheCleanCmd, cacheReportCmd)
	addCommand(cacheCmd)
}

func runCacheCompress(cmd *cobra.Command, args []string) error {
	c := cache.NewRootfs("")
	_, _, _ = c.Migrate()
	entries, err := c.Entries()
	if err != nil {
		return err
	}

	cutoff := time.Now().Add(-time.Duration(compressOlderThan) * 24 * time.Hour)
	var candidates []cache.Entry
	for _, e := range entries {
		if !e.Raw() {
			continue
		}
		if !compressAll && (e.LastUsed.IsZero() || e.LastUsed.After(cutoff)) {
			continue
		}
		candidates = append(candidates, e)
	}

	if len(candidates) == 0 {
		fmt.Println("No rootfs cache entries eligible for compression.")
		return nil
	}

	var estimatedBefore uint64
	for _, e := range candidates {
		estimatedBefore += e.DiskSize
		fmt.Printf("  %s  %.1f MiB raw  (last used %s)\n",
			digestShort(e.Digest), float64(e.DiskSize)/1024/1024, relTime(e.LastUsed))
	}

	if compressDryRun {
		fmt.Printf("(dry-run) would compress %d entries (%.1f MiB raw).\n",
			len(candidates), float64(estimatedBefore)/1024/1024)
		return nil
	}

	var freed uint64
	var failed int
	for _, e := range candidates {
		beforeRaw := e.DiskSize
		if err := c.Compress(e.Digest); err != nil {
			fmt.Fprintf(os.Stderr, "warn: compress %s: %v\n", digestShort(e.Digest), err)
			failed++
			continue
		}
		afterComp := c.SizeOfCompressed(e.Digest)
		if beforeRaw > afterComp {
			freed += beforeRaw - afterComp
		}
	}
	fmt.Printf("Compressed %d entries; freed %.1f MiB (%d failed).\n",
		len(candidates)-failed, float64(freed)/1024/1024, failed)
	return nil
}

func runCacheGC(cmd *cobra.Command, args []string) error {
	ws, err := config.ResolveWorkspace("")
	if err != nil {
		return err
	}
	merged := ws.Merged

	policy := cache.GCPolicy{}
	var toolsPolicy config.ToolCachePolicy
	if merged != nil && merged.Cache != nil {
		rp := merged.Cache.EffectiveRootfsPolicy()
		policy.MaxTotalBytes = rp.MaxSizeBytes()
		policy.MaxAge = rp.MaxAge()
		toolsPolicy = merged.Cache.EffectiveToolsPolicy()
	} else {
		// Sensible defaults for users who haven't customised siloconf.
		rp := (&config.CacheConfig{}).EffectiveRootfsPolicy()
		policy.MaxTotalBytes = rp.MaxSizeBytes()
		policy.MaxAge = rp.MaxAge()
		toolsPolicy = (&config.CacheConfig{}).EffectiveToolsPolicy()
	}
	if gcMaxSizeMB > 0 {
		policy.MaxTotalBytes = gcMaxSizeMB * 1024 * 1024
	}
	if gcMaxAgeDays > 0 {
		policy.MaxAge = time.Duration(gcMaxAgeDays) * 24 * time.Hour
	}

	rootfs := cache.NewRootfs("")
	_, _, _ = rootfs.Migrate()

	entries, err := rootfs.Entries()
	if err != nil {
		return err
	}
	var before uint64
	for _, e := range entries {
		before += e.DiskSize
	}
	fmt.Printf("Rootfs cache: %d entries, %.1f MiB on disk\n", len(entries), float64(before)/1024/1024)
	if policy.MaxTotalBytes > 0 {
		fmt.Printf("  size cap:  %d MiB\n", policy.MaxTotalBytes/1024/1024)
	}
	if policy.MaxAge > 0 {
		fmt.Printf("  age cap:   %s\n", policy.MaxAge)
	}

	if gcDryRun {
		simulated := simulateRootfsGC(entries, policy)
		if len(simulated) == 0 {
			fmt.Println("(dry-run) no rootfs evictions.")
		} else {
			fmt.Printf("(dry-run) would evict %d rootfs entries, freeing %.1f MiB:\n", len(simulated), freedSize(simulated))
			for _, e := range simulated {
				fmt.Printf("  %s  (%.1f MiB, last used %s)\n", digestShort(e.Digest), float64(e.DiskSize)/1024/1024, relTime(e.LastUsed))
			}
		}
	} else {
		res, err := rootfs.GC(policy)
		if err != nil {
			return err
		}
		if len(res.Evicted) == 0 {
			fmt.Println("Rootfs cache is within policy; nothing evicted.")
		} else {
			fmt.Printf("Evicted %d rootfs entries, freed %.1f MiB.\n", len(res.Evicted), float64(res.FreedBytes)/1024/1024)
		}
	}

	if gcImages {
		fmt.Println()
		if err := gcOCIImages(); err != nil {
			fmt.Fprintf(os.Stderr, "image GC failed: %v\n", err)
		}
	}

	if !gcToolCaches {
		return nil
	}

	fmt.Println()
	global, err := config.LoadGlobalConfig()
	if err != nil {
		return err
	}
	specs := buildToolCacheSpecs(global, toolsPolicy)
	if len(specs) == 0 {
		fmt.Println("No per-tool cache mounts to GC.")
		return nil
	}

	// Per-mount summary before action.
	for _, s := range specs {
		size := cache.DirDiskSize(s.HostPath)
		fmt.Printf("  %-24s %.1f MiB\n", s.Tool+"/"+s.Subdir, float64(size)/1024/1024)
	}

	if gcDryRun {
		fmt.Println("(dry-run) no tool-cache changes applied.")
		return nil
	}

	pc := cache.NewPkgCache(runtime.Cache())
	res, err := pc.GC(specs)
	if err != nil {
		return err
	}
	if len(res.Cleared) == 0 && res.FreedBytes == 0 {
		fmt.Println("Tool caches are within policy; nothing freed.")
	} else {
		fmt.Printf("Tool-cache GC freed %.1f MiB (cleared: %s).\n",
			float64(res.FreedBytes)/1024/1024, strings.Join(res.Cleared, ", "))
	}
	return nil
}

// gcOCIImages reports (and optionally reclaims) the bytes consumed by
// unreferenced layer blobs in the OCI image content store.
func gcOCIImages() error {
	mgr, err := bridge.NewManager(runtime.Kernel(), runtime.Initfs(), runtime.Root(), false)
	if err != nil {
		return fmt.Errorf("bridge unavailable: %w", err)
	}
	defer mgr.Close()

	if gcDryRun {
		size, err := mgr.ImageStoreOrphansSize()
		if err != nil {
			return err
		}
		fmt.Printf("OCI orphan blobs: %.1f MiB\n", float64(size)/1024/1024)
		fmt.Println("(dry-run) no image cleanup applied.")
		return nil
	}
	freed, err := mgr.ImageStoreCleanupOrphans()
	if err != nil {
		return err
	}
	if freed == 0 {
		fmt.Println("OCI content store: no orphan blobs.")
	} else {
		fmt.Printf("OCI content store: freed %.1f MiB of orphan blobs.\n", float64(freed)/1024/1024)
	}
	return nil
}

// buildToolCacheSpecs walks installed tools and produces a MountSpec per
// cache mount. Per-mount overrides in the policy replace the global size cap.
func buildToolCacheSpecs(global *config.GlobalConfig, pol config.ToolCachePolicy) []cache.MountSpec {
	if global == nil {
		return nil
	}
	home, _ := os.UserHomeDir()
	defaultSize := pol.MaxSizeBytes()
	age := pol.MaxAge()

	var out []cache.MountSpec
	for name, def := range global.Tools {
		for _, cm := range def.Cache {
			if cm.NoGC {
				continue
			}
			host := cm.Host
			if strings.HasPrefix(host, "~") {
				host = filepath.Join(home, strings.TrimPrefix(host, "~"))
			}
			// Derive a short mount label like "python/pip" from the host path.
			subdir := deriveMountSubdir(host, name)
			sizeCap := defaultSize
			if override, ok := pol.PerMount[name+"/"+subdir]; ok {
				sizeCap = override * 1024 * 1024
			} else if override, ok := pol.PerMount[name]; ok {
				sizeCap = override * 1024 * 1024
			}
			out = append(out, cache.MountSpec{
				Tool:         name,
				Subdir:       subdir,
				HostPath:     host,
				MaxSizeBytes: sizeCap,
				MaxAge:       age,
			})
		}
	}
	return out
}

// deriveMountSubdir strips the leading ~/.silo/cache/<tool>/ prefix from the
// host path to produce a concise label. Falls back to filepath.Base.
func deriveMountSubdir(host, tool string) string {
	prefix := filepath.Join(runtime.Cache(), tool) + string(filepath.Separator)
	if rest, ok := strings.CutPrefix(host, prefix); ok {
		return rest
	}
	return filepath.Base(host)
}

func simulateRootfsGC(entries []cache.Entry, pol cache.GCPolicy) []cache.Entry {
	var out []cache.Entry
	var total uint64
	for _, e := range entries {
		total += e.DiskSize
	}
	now := time.Now()
	kept := entries[:0]
	if pol.MaxAge > 0 {
		cutoff := now.Add(-pol.MaxAge)
		for _, e := range entries {
			if !e.LastUsed.IsZero() && e.LastUsed.Before(cutoff) {
				out = append(out, e)
				total -= e.DiskSize
				continue
			}
			kept = append(kept, e)
		}
	} else {
		kept = append(kept, entries...)
	}
	if pol.MaxTotalBytes > 0 {
		for total > pol.MaxTotalBytes && len(kept) > 0 {
			v := kept[0]
			kept = kept[1:]
			out = append(out, v)
			total -= v.DiskSize
		}
	}
	return out
}

func freedSize(entries []cache.Entry) float64 {
	var total uint64
	for _, e := range entries {
		total += e.DiskSize
	}
	return float64(total) / 1024 / 1024
}

func digestShort(d string) string {
	s := strings.TrimPrefix(d, "sha256:")
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

func relTime(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func runCacheClean(cmd *cobra.Command, args []string) error {
	if cacheCleanSafe {
		if cacheCleanAll || cacheCleanRootfs || cacheCleanContainers {
			return errs.Configf("--safe is mutually exclusive with --all/--rootfs/--containers")
		}
		return runCacheCleanSafe()
	}
	if !cacheCleanRootfs && !cacheCleanContainers {
		cacheCleanAll = true
	}
	if cacheCleanAll || cacheCleanRootfs {
		if err := cache.NewRootfs("").Clear(); err != nil {
			return err
		}
		fmt.Println("Cleared rootfs cache.")
	}
	if cacheCleanAll || cacheCleanContainers {
		_ = os.RemoveAll(runtime.Containers())
		fmt.Println("Cleared container state.")
	}
	return nil
}

func runCacheCleanSafe() error {
	global, err := config.LoadGlobalConfig()
	if err != nil {
		return err
	}
	installed := map[string]struct{}{}
	for name := range global.Tools {
		installed[name] = struct{}{}
	}

	rootfs := collectOrphanRootfs(global)
	toolCaches := collectOrphanToolCaches(installed)
	builds := collectOrphanBuilds(installed)
	containers := collectStaleContainers()

	buckets := []orphanBucket{
		{label: "Rootfs cache orphans", entries: rootfs},
		{label: "Per-tool cache orphans", entries: toolCaches},
		{label: "Build rootfs orphans", entries: builds},
		{label: "Stale container state", entries: containers},
	}

	total := uint64(0)
	nonEmpty := 0
	for _, b := range buckets {
		if len(b.entries) == 0 {
			continue
		}
		nonEmpty++
		bucketTotal := uint64(0)
		for _, e := range b.entries {
			bucketTotal += e.size
		}
		total += bucketTotal
		fmt.Printf("%s: %.1f MiB (%d)\n", b.label, float64(bucketTotal)/1024/1024, len(b.entries))
		for _, e := range b.entries {
			fmt.Printf("  %-50s %.1f MiB\n", e.description, float64(e.size)/1024/1024)
		}
	}
	if nonEmpty == 0 {
		fmt.Println("Nothing to clean. No orphan artifacts found.")
		return nil
	}
	fmt.Printf("\nTotal: %.1f MiB across %d bucket(s).\n", float64(total)/1024/1024, nonEmpty)

	if cacheCleanDryRun {
		fmt.Println("(dry-run) no changes applied")
		return nil
	}
	if !cacheCleanForce {
		ok, err := Prompter.AskYesNo("Remove these artifacts?", false)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(os.Stderr, "Aborted.")
			return nil
		}
	}

	freed := uint64(0)
	for _, b := range buckets {
		for _, e := range b.entries {
			if e.removeFn == nil {
				continue
			}
			if err := e.removeFn(); err != nil {
				fmt.Fprintf(os.Stderr, "warning: %s: %v\n", e.description, err)
				continue
			}
			freed += e.size
		}
	}
	fmt.Printf("Reclaimed %.1f MiB.\n", float64(freed)/1024/1024)
	return nil
}

type orphanBucket struct {
	label   string
	entries []orphanEntry
}

type orphanEntry struct {
	description string
	size        uint64
	removeFn    func() error
}

// collectOrphanRootfs returns rootfs-cache entries whose digest does not
// correspond to any installed tool's image. Requires the bridge to resolve
// image references to digests — if the runtime is missing, returns no entries
// rather than forcing a 5-minute bootstrap.
func collectOrphanRootfs(global *config.GlobalConfig) []orphanEntry {
	rootfs := cache.NewRootfs("")
	// Collapse any legacy `<hex>_<bytes>.ext4` files before scanning so the
	// orphan classification doesn't misfire on stale filename formats.
	_, _, _ = rootfs.Migrate()

	files, err := rootfs.List()
	if err != nil || len(files) == 0 {
		return nil
	}

	mgr, err := bridge.NewManager(runtime.Kernel(), runtime.Initfs(), runtime.Root(), false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "note: rootfs cache skipped (runtime unavailable: %v)\n", err)
		return nil
	}
	defer mgr.Close()

	// Set of digests referenced by installed tools.
	wanted := map[string]struct{}{}
	if global != nil {
		for _, def := range global.Tools {
			img, err := mgr.ImageGet(def.Image, false)
			if err != nil {
				continue
			}
			wanted[img.Digest()] = struct{}{}
			img.Close()
		}
	}

	var out []orphanEntry
	for _, path := range files {
		name := filepath.Base(path)
		digestHex := parseCacheFilename(name)
		if digestHex == "" {
			continue
		}
		if _, ok := wanted["sha256:"+digestHex]; ok {
			continue
		}
		st, err := os.Stat(path)
		if err != nil {
			continue
		}
		p := path
		out = append(out, orphanEntry{
			description: name,
			size:        uint64(st.Size()),
			removeFn:    func() error { return os.Remove(p) },
		})
	}
	return out
}

func collectOrphanToolCaches(installed map[string]struct{}) []orphanEntry {
	return collectOrphanSubdirs(runtime.Cache(), installed)
}

func collectOrphanBuilds(installed map[string]struct{}) []orphanEntry {
	return collectOrphanSubdirs(runtime.Builds(), installed)
}

func collectOrphanSubdirs(dir string, installed map[string]struct{}) []orphanEntry {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []orphanEntry
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, ok := installed[e.Name()]; ok {
			continue
		}
		full := filepath.Join(dir, e.Name())
		size := dirSize(full)
		p := full
		out = append(out, orphanEntry{
			description: fmt.Sprintf("%s (%s)", e.Name(), full),
			size:        size,
			removeFn:    func() error { return os.RemoveAll(p) },
		})
	}
	return out
}

func collectStaleContainers() []orphanEntry {
	dir := runtime.Containers()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []orphanEntry
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "silo-") {
			continue
		}
		full := filepath.Join(dir, e.Name())
		size := dirSize(full)
		p := full
		out = append(out, orphanEntry{
			description: e.Name(),
			size:        size,
			removeFn:    func() error { return os.RemoveAll(p) },
		})
	}
	return out
}

// parseCacheFilename extracts the hex digest from "<hex>.ext4". Returns ""
// for non-matching names (including legacy "<hex>_<bytes>.ext4" — those are
// handled by Migrate()).
func parseCacheFilename(name string) string {
	if !strings.HasSuffix(name, ".ext4") {
		return ""
	}
	stem := strings.TrimSuffix(name, ".ext4")
	if strings.Contains(stem, "_") {
		return ""
	}
	return stem
}
