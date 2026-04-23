// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rchekalov/silo/internal/bridge"
	"github.com/rchekalov/silo/internal/cache"
	"github.com/rchekalov/silo/internal/config"
	"github.com/rchekalov/silo/internal/engine"
	"github.com/rchekalov/silo/internal/errs"
	"github.com/rchekalov/silo/internal/runtime"
	"github.com/rchekalov/silo/internal/shim"
	"github.com/rchekalov/silo/internal/tools"
)

var (
	pullDryRun bool
	pullForce  bool
)

var syncCmd = &cobra.Command{
	Use:     "sync [path]",
	Aliases: []string{"pull", "apply"},
	Short:   "Reconcile the environment to match .siloconf (install + pull images)",
	Long: `Walks up from [path] (or cwd) for a .siloconf, then makes sure every tool it
references is installed globally and its image is pulled with a warm rootfs
cache. Safe to re-run; only missing pieces are fetched.

'pull' and 'apply' are kept as aliases for compatibility.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runSync,
}

func init() {
	syncCmd.Flags().BoolVar(&pullDryRun, "dry-run", false, "print the plan without acting")
	syncCmd.Flags().BoolVar(&pullForce, "force", false, "re-pull images even if rootfs cache is warm")
	addCommand(syncCmd)
}

func runSync(cmd *cobra.Command, args []string) error {
	if cmd.CalledAs() == "pull" || cmd.CalledAs() == "apply" {
		fmt.Fprintf(os.Stderr, "note: `silo %s` is now `silo sync`; the alias will be removed in 0.6.0.\n", cmd.CalledAs())
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
		fmt.Println("No tools declared in .siloconf (use `tools: [...]` or add entries under `overrides:`).")
		return nil
	}

	if root != "" {
		fmt.Printf("Reconciling %s\n", filepath.Join(root, config.ProjectConfigFilename))
	} else {
		fmt.Println("Reconciling global ~/.silo/siloconf")
	}
	fmt.Printf("Tools: %s\n\n", strings.Join(projectTools, ", "))

	global, err := config.LoadGlobalConfig()
	if err != nil {
		return err
	}

	plans, err := planProjectTools(projectTools, merged, global)
	if err != nil {
		return err
	}

	for _, p := range plans {
		fmt.Printf("  %-20s  %s\n", p.name, describePlan(p))
	}
	fmt.Println()

	if pullDryRun {
		fmt.Println("(dry-run) no changes applied")
		return nil
	}

	if needsRuntime(plans) {
		if err := runtime.EnsureDirectories(); err != nil {
			return err
		}
		e := engine.NewContainerEngine(global)
		if err := e.EnsureRuntime(); err != nil {
			return err
		}
	}

	var failed []string
	for _, p := range plans {
		if err := executePlan(p, global); err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", p.name, err))
			fmt.Fprintf(os.Stderr, "error: %s: %v\n", p.name, err)
			continue
		}
	}

	// Project-scoped bakes. Two triggers:
	//
	//   1. .siloconf adds extra postInstall steps for a tool -> bake a delta
	//      on top of the global rootfs (existing behaviour).
	//   2. The project's effective image differs from the globally installed
	//      image for the tool (e.g. `tools: [python@3.12]` while the global
	//      install is 3.14) -> bake the FULL postInstall chain from scratch
	//      against the pinned image. The global rootfs is for the wrong
	//      toolchain version and must not seed.
	//
	// The full-bake branch covers the LSP-version-match case: `lsp.install`
	// is merged into the registry postInstall at decode time, so a full
	// project bake against the pinned image produces a language server that
	// matches `silo run`'s toolchain. Both paths are idempotent via a
	// hash sidecar next to the project rootfs.
	if merged != nil && root != "" {
		for _, tool := range projectTools {
			if err := bakeProjectForTool(tool, merged, global, root); err != nil {
				failed = append(failed, fmt.Sprintf("%s (bake): %v", tool, err))
				fmt.Fprintf(os.Stderr, "error: %s (bake): %v\n", tool, err)
			}
		}
	}

	if len(failed) > 0 {
		return fmt.Errorf("pull completed with errors:\n  %s", strings.Join(failed, "\n  "))
	}
	fmt.Printf("\nPulled %d tool(s).\n", len(plans))
	return nil
}

// toolPlan captures what has to happen for one tool to be ready.
type toolPlan struct {
	name             string
	def              config.ToolDefinition // effective (global + override) definition
	installGlobally  bool                  // tool is not in ~/.silo/config.yaml yet
	needsPull        bool                  // rootfs cache is cold OR --force
	needsRootfsStore bool                  // pull will store rootfs (when cacheFor != nil)
	shimConflicts    []shim.Conflict
}

func planProjectTools(
	projectTools []string,
	merged *config.ProjectConfig,
	global *config.GlobalConfig,
) ([]toolPlan, error) {
	out := make([]toolPlan, 0, len(projectTools))

	// Opening the bridge manager to check rootfs-cache hits requires the runtime
	// to be ready. To keep plan construction pure (and avoid a 5-min bootstrap
	// during `--dry-run`), defer the cache-warmth check and mark `needsPull`
	// optimistically; executePlan re-checks.
	sm := shim.NewManager("")

	for _, name := range projectTools {
		def, installed := resolvePullDef(name, merged, global)
		if def.Image == "" {
			return nil, errs.ToolNotFoundError(name)
		}
		p := toolPlan{
			name:             name,
			def:              def,
			installGlobally:  !installed,
			needsPull:        true, // checked for real in executePlan
			needsRootfsStore: true,
		}
		if !installed {
			p.shimConflicts = sm.CheckConflicts(def, name, global)
		}
		out = append(out, p)
	}
	return out, nil
}

// resolvePullDef returns (effectiveDef, installedGlobally). If the tool is in
// the global config, that wins; otherwise we fall through to the registry.
// Either way, project overrides from merged.Overrides are applied on top.
func resolvePullDef(
	name string,
	merged *config.ProjectConfig,
	global *config.GlobalConfig,
) (config.ToolDefinition, bool) {
	var def config.ToolDefinition
	installed := false
	if global != nil {
		if t, ok := global.Tools[name]; ok {
			def = t
			installed = true
		}
	}
	if !installed {
		if t, ok, _ := tools.Lookup(name, ""); ok {
			def = t
		}
	}
	if merged != nil {
		if o, ok := merged.Overrides[name]; ok {
			def = config.ApplyOverride(def, o)
		}
	}
	return def, installed
}

func describePlan(p toolPlan) string {
	parts := []string{}
	if p.installGlobally {
		parts = append(parts, "install")
	}
	parts = append(parts, "pull "+p.def.Image)
	if len(p.shimConflicts) > 0 {
		names := make([]string, 0, len(p.shimConflicts))
		for _, c := range p.shimConflicts {
			names = append(names, fmt.Sprintf("%s (used by %s)", c.Shim, c.OtherTool))
		}
		parts = append(parts, "⚠ shim conflicts: "+strings.Join(names, ", "))
	}
	return strings.Join(parts, "; ")
}

func needsRuntime(plans []toolPlan) bool {
	return len(plans) > 0
}

func executePlan(p toolPlan, global *config.GlobalConfig) error {
	sm := shim.NewManager("")
	if p.installGlobally {
		// Report shim conflicts as warnings (matches install.go semantics).
		for _, c := range p.shimConflicts {
			fmt.Fprintf(os.Stderr, "warning: shim %q conflicts with tool %q\n", c.Shim, c.OtherTool)
		}
		if err := sm.CreateShims(p.def, p.name); err != nil {
			return err
		}
		if err := global.InstallTool(p.name, p.def); err != nil {
			return err
		}
	}

	e := engine.NewContainerEngine(global)

	// Cheap rootfs-cache probe: if --force is not set and we can confirm a hit,
	// skip the pull entirely. Require the bridge manager, which in turn requires
	// the runtime; EnsureRuntime was called by the caller for this reason.
	if !pullForce {
		if hit, err := rootfsCacheHit(p.def); err == nil && hit {
			fmt.Printf("  %-20s  rootfs cache warm — skipping pull\n", p.name)
			return nil
		}
	}

	return e.PullImage(p.def.Image, &p.def)
}

// rootfsCacheHit reports whether the cached rootfs for `def` already exists.
// Returns (false, nil) if the image is not yet in the local OCI store (i.e.
// the digest cannot be resolved without a pull). Errors propagate.
func rootfsCacheHit(def config.ToolDefinition) (bool, error) {
	mgr, err := bridge.NewManager(runtime.Kernel(), runtime.Initfs(), runtime.Root(), false)
	if err != nil {
		return false, err
	}
	defer mgr.Close()
	img, err := mgr.ImageGet(def.Image, false)
	if err != nil {
		// Image not locally known yet — definitely needs a pull.
		return false, nil
	}
	defer img.Close()
	digest := img.Digest()
	size := def.RootfsSizeMB * 1024 * 1024
	return cache.NewRootfs("").Has(digest, size), nil
}

// bakeProjectForTool is the sync-time bake dispatcher. It picks between:
//
//   - Full bake from scratch, when the project pins a different image version
//     than the global install (the global rootfs was produced against the
//     wrong toolchain and can't seed the bake).
//   - Delta bake on top of the global rootfs, when .siloconf adds extra
//     postInstall steps for the tool.
//   - No-op, when neither applies.
//
// `silo add` keeps calling `bakeProjectPostInstallFor` directly because it
// always operates on the existing install's image (adding packages, not
// changing the version).
func bakeProjectForTool(tool string, merged *config.ProjectConfig, global *config.GlobalConfig, root string) error {
	if merged == nil || root == "" {
		return nil
	}
	globalDef, installed := global.Tools[tool]
	if !installed {
		// `silo sync`'s planner installs globally before this runs, so a
		// missing entry here indicates a planner error. Surface rather
		// than silently skip the bake.
		return fmt.Errorf("tool %q is not installed; install aborted earlier", tool)
	}
	def, _ := resolvePullDef(tool, merged, global)
	if def.Image != globalDef.Image {
		e := engine.NewContainerEngine(global)
		if err := e.EnsureRuntime(); err != nil {
			return err
		}
		baked, err := tools.ApplyProjectFullBake(bakeAdapter(e), tool, def, def.PostInstall, root)
		if err != nil {
			return err
		}
		if baked {
			fmt.Printf("  %-20s  baked project rootfs at %s (pinned %s)\n", tool, runtime.ProjectRootfs(root, tool), def.Image)
		} else {
			fmt.Printf("  %-20s  project rootfs up-to-date (pinned %s)\n", tool, def.Image)
		}
		return nil
	}
	return bakeProjectPostInstallFor(tool, merged, global, root)
}

// bakeProjectPostInstallFor runs a project-scoped bake for a single tool
// when the merged .siloconf contains extra postInstall steps. Shared between
// `silo sync` and `silo add` so both surface identical behaviour.
func bakeProjectPostInstallFor(tool string, merged *config.ProjectConfig, global *config.GlobalConfig, root string) error {
	if merged == nil || root == "" {
		return nil
	}
	override, ok := merged.Overrides[tool]
	if !ok || len(override.PostInstall) == 0 {
		return nil
	}
	if _, installed := global.Tools[tool]; !installed {
		// Project config references a tool that isn't globally installed.
		// The `silo sync` planner handles install first, so this is only
		// reachable when a caller (e.g. `silo add`) invokes the bake for a
		// tool the user hasn't installed yet. Surface the mismatch.
		return fmt.Errorf("tool %q is not installed; run `silo install %s` first", tool, tool)
	}
	def, _ := resolvePullDef(tool, merged, global)
	e := engine.NewContainerEngine(global)
	if err := e.EnsureRuntime(); err != nil {
		return err
	}
	baked, err := tools.ApplyProjectPostInstall(bakeAdapter(e), tool, def, override.PostInstall, root)
	if err != nil {
		return err
	}
	if baked {
		fmt.Printf("  %-20s  baked project rootfs at %s\n", tool, runtime.ProjectRootfs(root, tool))
	} else {
		fmt.Printf("  %-20s  project rootfs up-to-date\n", tool)
	}
	return nil
}

func resolvedStart(start string) string {
	if start == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "."
		}
		return cwd
	}
	abs, err := filepath.Abs(start)
	if err != nil {
		return start
	}
	return abs
}
