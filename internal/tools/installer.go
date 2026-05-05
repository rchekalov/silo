// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/rchekalov/silo/internal/config"
	"github.com/rchekalov/silo/internal/errs"
	"github.com/rchekalov/silo/internal/prompter"
	"github.com/rchekalov/silo/internal/runtime"
	"github.com/rchekalov/silo/internal/shim"
)

// BakeFunc runs a setup command in a writable VM seeded from the tool's image
// (or the global build rootfs when Global is false and one exists) and
// snapshots the result at `target` on exit 0. It's the single hook shared by
// install-time postInstall and project-scoped `silo sync` bakes.
type BakeFunc func(toolName string, tool config.ToolDefinition, command string, args []string, target string, global bool) (int32, error)

// Installer orchestrates adding and removing tools. It consolidates what used
// to live inline in commands/install.go so the same flow is reachable from
// scripted paths (e.g., `silo init --install-detected`, future batch ops).
//
// The engine is accessed through two injected funcs so tools/ doesn't need to
// import internal/engine (which already imports tools — see discovery.go).
type Installer struct {
	Config   *config.GlobalConfig
	Shims    *shim.Manager
	Prompter prompter.Prompter

	// EnsureRuntime runs the kernel/initfs bootstrap. Defaults to no-op if nil.
	EnsureRuntime func() error
	// PullImage pulls `reference` and caches the rootfs for the given tool.
	PullImage func(reference string, tool *config.ToolDefinition) error
	// RunCaptured is used by auto-discovery. If nil, discovery is skipped.
	RunCaptured CaptureRunFunc
	// RunSetup runs a setup command in a writable VM and persists the rootfs
	// at `target`. Used to bake registry-level `postInstall:` scripts during
	// install. If nil, postInstall scripts are skipped with a warning.
	RunSetup BakeFunc
}

// ReservedNames cannot be used as tool names — they clash with subcommands.
var ReservedNames = map[string]struct{}{
	"install": {}, "uninstall": {}, "list": {}, "run": {}, "shell": {},
	"status": {}, "setup": {}, "rebuild": {}, "cache": {}, "reset": {},
	"help": {}, "lsp": {}, "init": {}, "ide": {}, "shim": {}, "config": {},
}

// InstallOptions controls one install call.
type InstallOptions struct {
	Name    string
	Version string   // registry tag; ignored when Image is set
	Image   string   // non-empty = custom image; Shims may be empty to trigger discovery
	Shims   []string // explicit shim specs (overrides discovery)
	Force   bool     // reinstall even if already installed
}

// Install adds or replaces a tool. Returns the final ToolDefinition written.
func (in *Installer) Install(opts InstallOptions) (config.ToolDefinition, error) {
	if _, reserved := ReservedNames[opts.Name]; reserved {
		return config.ToolDefinition{}, errs.Configf("%q is a reserved silo subcommand and cannot be a tool name", opts.Name)
	}

	_, alreadyInstalled := in.Config.Tools[opts.Name]
	if alreadyInstalled && !opts.Force {
		return config.ToolDefinition{}, errs.ToolAlreadyInstalledError(opts.Name)
	}

	def, err := in.resolveDefinition(opts)
	if err != nil {
		return def, err
	}

	if err := in.validateRequires(def); err != nil {
		return def, err
	}

	if err := runtime.EnsureDirectories(); err != nil {
		return def, err
	}
	if in.EnsureRuntime != nil {
		if err := in.EnsureRuntime(); err != nil {
			return def, err
		}
	}

	// Discovery for --image with no --shim.
	if opts.Image != "" && len(opts.Shims) == 0 && in.RunCaptured != nil {
		if err := in.autoDiscoverShims(&def, opts.Name); err != nil {
			return def, err
		}
	}

	if alreadyInstalled && opts.Force {
		_ = in.Shims.RemoveShims(in.Config.Tools[opts.Name])
	}

	conflicts := in.Shims.CheckConflicts(def, opts.Name, in.Config)
	if len(conflicts) > 0 {
		fmt.Fprintln(os.Stderr, "Warning: the following shims will be overwritten:")
		for _, c := range conflicts {
			fmt.Fprintf(os.Stderr, "  %q is currently owned by %q\n", c.Shim, c.OtherTool)
		}
		ok, err := in.Prompter.AskYesNo("Continue?", false)
		if err != nil {
			return def, err
		}
		if !ok {
			return def, errs.Configf("installation cancelled")
		}
	}

	// Pull the image before we publish the tool to config + shims. The pull
	// is the long step the user might cancel; if we registered the tool
	// first, a ^C would leave `silo list` claiming the tool is installed
	// when the rootfs isn't actually on disk. Order:
	//   1. pull image (cancellable)
	//   2. create shims
	//   3. write config entry
	// An interrupted pull leaves nothing behind to clean up.
	if in.PullImage != nil {
		if err := in.PullImage(def.Image, &def); err != nil {
			return def, err
		}
	}

	if err := in.runPostInstall(&def, opts.Name); err != nil {
		return def, err
	}

	if err := in.Shims.CreateShims(def, opts.Name); err != nil {
		return def, err
	}
	WarnIfShimsShadowed(def, os.Stderr)
	// `silo install` is the explicit "silo owns this command everywhere" gesture,
	// so the entry is globally pinned. `silo sync` writes through a different
	// path (commands/pull.go) that leaves the flag false — those tools fall
	// through to the next-on-PATH instance outside projects that claim them.
	def.PinnedGlobally = true
	if err := in.Config.InstallTool(opts.Name, def); err != nil {
		return def, err
	}

	warnIfShimBinNotOnPATH()
	return def, nil
}

// Uninstall removes shims and the config entry. No error on missing.
func (in *Installer) Uninstall(name string) error {
	def, ok := in.Config.Tools[name]
	if !ok {
		return errs.ToolNotInstalledError(name)
	}
	if err := in.Shims.RemoveShims(def); err != nil {
		return err
	}
	return in.Config.UninstallTool(name)
}

// resolveDefinition turns InstallOptions into a ToolDefinition.
func (in *Installer) resolveDefinition(opts InstallOptions) (config.ToolDefinition, error) {
	if opts.Image != "" {
		def := config.NewToolDefinition()
		def.Image = opts.Image
		if len(opts.Shims) > 0 {
			def.Shims = make([]config.ShimMapping, 0, len(opts.Shims))
			for _, s := range opts.Shims {
				def.Shims = append(def.Shims, config.ParseShim(s))
			}
		}
		return def, nil
	}
	def, ok, err := Lookup(opts.Name, opts.Version)
	if err != nil {
		return def, err
	}
	if !ok {
		return def, errs.ToolNotFoundError(opts.Name)
	}
	return def, nil
}

// validateRequires ensures every `requires` entry is resolvable (installed or
// in the registry). Matches the Swift implementation's check.
func (in *Installer) validateRequires(def config.ToolDefinition) error {
	for _, dep := range def.Requires {
		if _, ok := in.Config.Tools[dep]; ok {
			continue
		}
		if _, ok, _ := Lookup(dep, ""); ok {
			continue
		}
		return errs.Configf("requires %q which is not available; install it first: silo install %s", dep, dep)
	}
	return nil
}

// autoDiscoverShims fills def.Shims by booting the image and scanning PATH.
func (in *Installer) autoDiscoverShims(def *config.ToolDefinition, name string) error {
	fmt.Fprintln(os.Stderr, "Scanning image for executables…")
	names, err := DiscoverExecutables(in.RunCaptured, name, *def)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		fmt.Fprintln(os.Stderr, "No non-system executables found. Using tool name as shim.")
		def.Shims = []config.ShimMapping{config.ParseShim(name)}
		return nil
	}
	fmt.Fprintf(os.Stderr, "Found %d executables: %v\n", len(names), names)
	ok, err := in.Prompter.AskYesNo("Install shims for all of them?", true)
	if err != nil {
		return err
	}
	if !ok {
		def.Shims = []config.ShimMapping{config.ParseShim(name)}
		return nil
	}
	for _, n := range names {
		def.Shims = append(def.Shims, config.ParseShim(n))
	}
	return nil
}

// BakeOptions parameterizes BakeTool.
type BakeOptions struct {
	Name        string
	Def         config.ToolDefinition
	Steps       []string // shell fragments joined with " && " + `sync`
	Target      string   // rootfs.ext4 path to persist to
	Scope       string   // "global" or "project"
	ProjectRoot string   // required when Scope == "project"
	// FromScratch forces the bake VM to ignore any existing global rootfs and
	// start from the pinned image cache. Only meaningful when Scope=="project";
	// needed when the project pins a different image version than the global
	// install, so seeding from the wrong-version global rootfs doesn't poison
	// the project bake.
	FromScratch bool
}

// BakeTool runs opts.Steps in a writable VM and snapshots the resulting
// rootfs into opts.Target on exit 0. Networking is on for the build, with the
// proxy allowlist broadened by `installAllow` (apt repos, etc.) — see
// setupProxyRule in engine/ephemeral.go. The caller's original def is not
// mutated — the returned ToolDefinition has BuildRootfs / BuildScript /
// BuildScope / BuildProjectRoot populated.
//
// run must be non-nil. If opts.Steps is empty the function is a no-op and
// returns opts.Def unchanged.
func BakeTool(run BakeFunc, opts BakeOptions) (config.ToolDefinition, error) {
	if len(opts.Steps) == 0 {
		return opts.Def, nil
	}
	if run == nil {
		return opts.Def, fmt.Errorf("bake: no RunSetup hook available")
	}
	if opts.Scope == "project" && opts.ProjectRoot == "" {
		return opts.Def, fmt.Errorf("bake: project scope requires ProjectRoot")
	}

	// Append `sync` so the guest flushes its page cache to the backing ext4
	// block device before `sh -c` exits. Without it, RunSetup snapshots the
	// rootfs while the VM is still running, and the tail end of a fast write
	// burst (e.g. `npm install -g` that finishes in ~12s) never reaches disk
	// — the installed package is silently lost from the baked rootfs.
	script := strings.Join(opts.Steps, " && ") + " && sync"
	if err := os.MkdirAll(filepath.Dir(opts.Target), 0o755); err != nil {
		return opts.Def, fmt.Errorf("bake: prepare build dir: %w", err)
	}

	// Force networking on for the build stage even when the tool's runtime
	// config doesn't ask for host access. setupProxyRule (in the engine) will
	// union proxy.Allow ∪ proxy.InstallAllow so registry-declared apt/npm/pip
	// upstreams are reachable during postInstall and dropped at runtime.
	buildDef := opts.Def
	if buildDef.Network == nil {
		buildDef.Network = &config.NetworkConfig{HostAccess: true}
	} else if !buildDef.Network.HostAccess {
		n := *buildDef.Network
		n.HostAccess = true
		buildDef.Network = &n
	}

	// The RunSetup hook interprets its final argument as "don't seed from the
	// global rootfs cache" — true means cold-create from the image. For a
	// project-scoped bake that crosses a version boundary, we want the same
	// cold start even though the scope is "project".
	global := opts.Scope == "global" || opts.FromScratch
	exit, err := run(opts.Name, buildDef, "sh", []string{"-c", script}, opts.Target, global)
	if err != nil {
		return opts.Def, fmt.Errorf("bake: %w", err)
	}
	if exit != 0 {
		return opts.Def, fmt.Errorf("bake: setup exited %d", exit)
	}

	out := opts.Def
	out.BuildRootfs = opts.Target
	out.BuildScript = "sh -c " + script
	out.BuildScope = opts.Scope
	if opts.Scope == "project" {
		out.BuildProjectRoot = opts.ProjectRoot
	} else {
		out.BuildProjectRoot = ""
	}
	return out, nil
}

// runPostInstall bakes the tool's registry-level postInstall script into a
// persistent global rootfs. Thin wrapper over BakeTool kept for clarity at
// the install callsite.
func (in *Installer) runPostInstall(def *config.ToolDefinition, name string) error {
	if len(def.PostInstall) == 0 {
		return nil
	}
	if in.RunSetup == nil {
		fmt.Fprintf(os.Stderr, "warning: %s has postInstall steps but no RunSetup hook; skipping\n", name)
		return nil
	}
	fmt.Fprintf(os.Stderr, "Running postInstall for %s...\n", name)
	updated, err := BakeTool(in.RunSetup, BakeOptions{
		Name:   name,
		Def:    *def,
		Steps:  def.PostInstall,
		Target: runtime.GlobalBuildRootfs(name),
		Scope:  "global",
	})
	if err != nil {
		return fmt.Errorf("postInstall: %w", err)
	}
	*def = updated
	return nil
}

// warnIfShimBinNotOnPATH emits a one-liner hint if ~/.silo/bin isn't on $PATH.
func warnIfShimBinNotOnPATH() {
	shimBin := runtime.ShimBin()
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		if p == shimBin {
			return
		}
	}
	fmt.Fprintf(os.Stderr, "Hint: add %s to your PATH to use shims directly.\n", shimBin)
}

// WarnIfShimsShadowed checks each shim in def against the live $PATH and
// warns if some other directory's binary outranks ~/.silo/bin/<shim>. Catches
// the common case where ~/.zshrc evals brew/conda/asdf shellenv *after* silo,
// which silently re-prepends and shadows our shims (e.g. `pip` resolves to
// /opt/homebrew/bin/pip even though ~/.silo/bin/pip exists).
//
// Best-effort: exec.LookPath reads the inherited $PATH, which matches what
// silo saw when it was invoked. A fully accurate check would spawn a login
// shell, which isn't worth it for a hint. Exported so `silo doctor` can reuse.
func WarnIfShimsShadowed(def config.ToolDefinition, w *os.File) {
	shimBin := runtime.ShimBin()
	for _, s := range def.Shims {
		ours := filepath.Join(shimBin, s.HostCommand)
		resolved, err := exec.LookPath(s.HostCommand)
		if err != nil || resolved == ours {
			continue
		}
		// Resolve symlinks on both sides — homebrew's pip is often a symlink
		// chain into a versioned cellar path; comparing the realpath avoids
		// false negatives if the user symlinked silo's shim into another bin.
		resolvedReal, _ := filepath.EvalSymlinks(resolved)
		oursReal, _ := filepath.EvalSymlinks(ours)
		if resolvedReal != "" && resolvedReal == oursReal {
			continue
		}
		fmt.Fprintf(w,
			"Hint: %s exists but `which %s` resolves to %s.\n"+
				"      Your shell prepends another bin dir after silo's. Run\n"+
				"      `eval \"$(silo shellenv)\"` AFTER your homebrew/conda/asdf\n"+
				"      init in ~/.zshrc / ~/.bashrc so silo wins.\n",
			ours, s.HostCommand, resolved)
	}
}
