// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/rchekalov/silo/internal/bridge"
	"github.com/rchekalov/silo/internal/cache"
	"github.com/rchekalov/silo/internal/config"
	"github.com/rchekalov/silo/internal/errs"
	"github.com/rchekalov/silo/internal/runtime"
	"github.com/spf13/cobra"
)

var (
	cleanRootfsOnly bool
	cleanCachesOnly bool
	cleanForce      bool
	cleanShared     bool
	cleanKeepShared bool
	cleanDryRun     bool
)

var cleanCmd = &cobra.Command{
	Use:   "clean [path]",
	Short: "Reclaim disk tied to a project (rootfs cache, package caches, stale VMs)",
	Long: `Walks up from [path] (or cwd) for a .siloconf, then removes heavy artifacts
tied to the project's tools:
  - rootfs cache entries for each tool's image
  - per-tool package caches (pip, npm, cargo, ...)
  - stale container directories left behind by crashed runs

Does NOT uninstall tools or touch shims/config — the next 'silo pull' brings
the project back to a working state.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runClean,
}

func init() {
	cleanCmd.Flags().BoolVar(&cleanRootfsOnly, "rootfs-only", false, "only remove rootfs cache entries")
	cleanCmd.Flags().BoolVar(&cleanCachesOnly, "caches-only", false, "only remove per-tool package caches")
	cleanCmd.Flags().BoolVar(&cleanForce, "force", false, "skip confirmation, remove shared artifacts too")
	cleanCmd.Flags().BoolVar(&cleanShared, "shared", false, "alias for --force (explicit about removing shared)")
	cleanCmd.Flags().BoolVar(&cleanKeepShared, "keep-shared", false, "non-interactively skip artifacts shared with other tools")
	cleanCmd.Flags().BoolVar(&cleanDryRun, "dry-run", false, "print the plan without acting")
	addCommand(cleanCmd)
}

// cleanBucket captures one classified set of artifacts to remove.
type cleanBucket struct {
	label    string
	entries  []cleanEntry
	doRemove func(cleanEntry) error
}

type cleanEntry struct {
	description string
	size        uint64
	shared      bool // true if this artifact is also referenced by a tool not in the project set
	removeFn    func() error
}

func runClean(cmd *cobra.Command, args []string) error {
	if cleanRootfsOnly && cleanCachesOnly {
		return errs.Configf("--rootfs-only and --caches-only are mutually exclusive")
	}

	start := ""
	if len(args) == 1 {
		start = args[0]
	}
	ws, err := config.ResolveWorkspace(start)
	if err != nil {
		return err
	}
	merged := ws.Merged
	root := ws.ProjectRoot
	if merged == nil {
		return errs.Configf("no .siloconf found (walked up from %q)", resolvedStart(start))
	}
	projectTools := merged.ProjectTools()
	if len(projectTools) == 0 {
		fmt.Println("No tools declared in .siloconf — nothing to clean.")
		return nil
	}
	if root != "" {
		fmt.Printf("Cleaning artifacts for %s\n", filepath.Join(root, config.ProjectConfigFilename))
	}
	fmt.Printf("Tools: %s\n\n", strings.Join(projectTools, ", "))

	global, err := config.LoadGlobalConfig()
	if err != nil {
		return err
	}

	projectSet := toSet(projectTools)
	projectDefs := effectiveDefs(projectTools, merged, global)

	var buckets []cleanBucket
	switch {
	case cleanRootfsOnly:
		b, err := collectRootfsBucket(projectDefs, projectSet, global)
		if err != nil {
			return err
		}
		buckets = append(buckets, b)
	case cleanCachesOnly:
		buckets = append(buckets, collectToolCacheBucket(projectTools))
	default:
		b, err := collectRootfsBucket(projectDefs, projectSet, global)
		if err != nil {
			return err
		}
		buckets = append(buckets, b)
		buckets = append(buckets, collectToolCacheBucket(projectTools))
		buckets = append(buckets, collectStaleContainerBucket())
		// OCI image content (~/.silo/images) is left untouched for now — the
		// bridge doesn't expose a delete API, and walking the content store
		// blindly risks corrupting other tools' images.
	}

	printBuckets(buckets)

	if cleanDryRun {
		fmt.Println("(dry-run) no changes applied")
		return nil
	}

	shared := filterShared(buckets)
	removeShared := true
	switch {
	case cleanForce || cleanShared:
		removeShared = true
	case cleanKeepShared:
		removeShared = false
	case len(shared) > 0:
		ok, err := Prompter.AskYesNo("Remove shared artifacts too?", false)
		if err != nil {
			return err
		}
		removeShared = ok
	}

	totalFreed := uint64(0)
	for _, b := range buckets {
		for _, e := range b.entries {
			if e.removeFn == nil {
				continue
			}
			if e.shared && !removeShared {
				continue
			}
			if err := e.removeFn(); err != nil {
				fmt.Fprintf(os.Stderr, "warning: %s: %v\n", e.description, err)
				continue
			}
			totalFreed += e.size
		}
	}
	fmt.Printf("\nReclaimed %.1f MiB.\n", float64(totalFreed)/1024/1024)
	return nil
}

func effectiveDefs(
	names []string, merged *config.ProjectConfig, global *config.GlobalConfig,
) map[string]config.ToolDefinition {
	out := map[string]config.ToolDefinition{}
	for _, name := range names {
		def, _ := resolvePullDef(name, merged, global)
		if def.Image == "" {
			continue
		}
		out[name] = def
	}
	return out
}

// collectRootfsBucket resolves each project tool's image to a digest via the
// local OCI store, then builds one entry per matched rootfs cache file. If the
// bridge cannot be initialised (e.g. runtime missing), we degrade gracefully
// and report no entries — `silo clean` should not trigger a 5-minute bootstrap.
func collectRootfsBucket(
	projectDefs map[string]config.ToolDefinition,
	projectSet map[string]struct{},
	global *config.GlobalConfig,
) (cleanBucket, error) {
	b := cleanBucket{label: "Rootfs cache"}
	mgr, err := bridge.NewManager(runtime.Kernel(), runtime.Initfs(), runtime.Root(), false)
	if err != nil {
		b.entries = append(b.entries, cleanEntry{
			description: fmt.Sprintf("runtime unavailable — skipping rootfs cache (%v)", err),
		})
		return b, nil
	}
	defer mgr.Close()

	rootfs := cache.NewRootfs("")
	_, _, _ = rootfs.Migrate()

	// Map of digest we care about → a human label (tool name + image).
	wanted := map[string]string{}
	for name, def := range projectDefs {
		img, err := mgr.ImageGet(def.Image, false)
		if err != nil {
			continue
		}
		wanted[img.Digest()] = fmt.Sprintf("%s (%s)", name, def.Image)
		img.Close()
	}

	// Global tools NOT in the project set — used to mark entries as "shared".
	other := map[string]struct{}{}
	if global != nil {
		for name, def := range global.Tools {
			if _, isProject := projectSet[name]; isProject {
				continue
			}
			img, err := mgr.ImageGet(def.Image, false)
			if err != nil {
				continue
			}
			other[img.Digest()] = struct{}{}
			img.Close()
		}
	}

	for digest, label := range wanted {
		path := rootfs.Path(digest, 0)
		st, err := os.Stat(path)
		if err != nil {
			continue
		}
		_, sharedWithOther := other[digest]
		d := digest // capture
		b.entries = append(b.entries, cleanEntry{
			description: label,
			size:        uint64(st.Size()),
			shared:      sharedWithOther,
			removeFn: func() error {
				return rootfs.RemoveByDigest(d, 0)
			},
		})
	}
	return b, nil
}

func collectToolCacheBucket(projectTools []string) cleanBucket {
	b := cleanBucket{label: "Per-tool package caches"}
	for _, name := range projectTools {
		dir := filepath.Join(runtime.Cache(), name)
		size := dirSize(dir)
		if size == 0 {
			continue
		}
		d := dir // capture
		b.entries = append(b.entries, cleanEntry{
			description: fmt.Sprintf("%s (%s)", name, dir),
			size:        size,
			removeFn:    func() error { return os.RemoveAll(d) },
		})
	}
	return b
}

func collectStaleContainerBucket() cleanBucket {
	b := cleanBucket{label: "Stale container state"}
	dir := runtime.Containers()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return b
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "silo-") {
			continue
		}
		full := filepath.Join(dir, e.Name())
		size := dirSize(full)
		p := full
		b.entries = append(b.entries, cleanEntry{
			description: e.Name(),
			size:        size,
			removeFn:    func() error { return os.RemoveAll(p) },
		})
	}
	return b
}

func printBuckets(buckets []cleanBucket) {
	var shared []cleanEntry
	for _, b := range buckets {
		if len(b.entries) == 0 {
			continue
		}
		total := uint64(0)
		for _, e := range b.entries {
			total += e.size
			if e.shared {
				shared = append(shared, e)
			}
		}
		fmt.Printf("%s: %.1f MiB (%d item(s))\n", b.label, float64(total)/1024/1024, len(b.entries))
		for _, e := range b.entries {
			marker := "  "
			if e.shared {
				marker = "s "
			}
			fmt.Printf("  %s%-40s %.1f MiB\n", marker, e.description, float64(e.size)/1024/1024)
		}
	}
	if len(shared) > 0 {
		fmt.Println()
		fmt.Println("Shared with other tools (marked 's'):")
		for _, e := range shared {
			fmt.Printf("  %s\n", e.description)
		}
	}
	fmt.Println()
}

func filterShared(buckets []cleanBucket) []cleanEntry {
	var out []cleanEntry
	for _, b := range buckets {
		for _, e := range b.entries {
			if e.shared {
				out = append(out, e)
			}
		}
	}
	return out
}

func toSet(names []string) map[string]struct{} {
	out := make(map[string]struct{}, len(names))
	for _, n := range names {
		out[n] = struct{}{}
	}
	return out
}

// dirSize returns the on-disk (blocks-based) size of `path`, summed over all
// non-directory entries. Reflects real disk usage on filesystems with sparse
// files (ext4 rootfs caches are sparse and would be vastly overestimated by
// the apparent Size()).
func dirSize(path string) uint64 {
	var total uint64
	_ = filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if sys, ok := info.Sys().(*syscall.Stat_t); ok {
			total += uint64(sys.Blocks) * 512
			return nil
		}
		total += uint64(info.Size())
		return nil
	})
	return total
}
