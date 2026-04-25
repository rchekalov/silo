// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/rchekalov/silo/internal/bridge"
	"github.com/rchekalov/silo/internal/config"
	"github.com/rchekalov/silo/internal/errs"
	"github.com/rchekalov/silo/internal/lsp"
	"github.com/rchekalov/silo/internal/runtime"
	"github.com/rchekalov/silo/internal/tools"
)

// RunLSPOptions configures RunLSP.
type RunLSPOptions struct {
	ToolName      string
	Tool          config.ToolDefinition
	ProjectDir    string
	ProjectRoot   string
	ProjectConfig *config.ProjectConfig
}

// RunLSP runs the LSP server in an ephemeral VM, bridging stdio through a
// path-rewriting proxy. Returns when the IDE closes stdin or the server exits.
func (e *ContainerEngine) RunLSP(opts RunLSPOptions) (int32, error) {
	if opts.Tool.LSP == nil {
		return -1, errs.Configf("tool %q has no LSP configuration", opts.ToolName)
	}
	r := newEphemeralRunner(runtime.Kernel(), runtime.Initfs(), runtime.Root())
	return r.RunLSP(opts)
}

// RunLSP is the backing implementation for ContainerEngine.RunLSP.
func (r *ephemeralRunner) RunLSP(opts RunLSPOptions) (int32, error) {
	id := fmt.Sprintf("silo-lsp-%s-%s", opts.ToolName, shortID())
	lspCfg := opts.Tool.LSP

	// Apply project-level resource overrides first so the LSP floor below
	// only kicks in when the (possibly overridden) value is still too small.
	applyResourceOverrides(&opts.Tool, opts.ToolName, opts.ProjectConfig)
	// Language servers are memory-hungry — pyright-langserver loads all of
	// typeshed plus the project via a node runtime, gopls tracks module
	// dependency graphs, rust-analyzer mirrors the whole crate graph. The
	// tool's default 512 MB (good enough for a quick `silo run`) silently
	// OOMs a fresh LSP VM, so lift the floor here without touching the
	// stored definition.
	applyLspResourceFloors(&opts.Tool)

	effectiveNet, _, imageRef := resolveOverrides(opts.Tool, opts.ToolName, opts.ProjectConfig)
	needsNet := effectiveNet != nil && effectiveNet.HostAccess

	// Point the user at the right recovery before we hit the opaque
	// "failed to find target executable" error from vminitd.
	warnIfLspBakeMissing(opts, imageRef)

	mgr, err := r.newManager(needsNet)
	if err != nil {
		return -1, err
	}
	defer mgr.Close()

	// Build env (LSP-specific): force TERM=dumb, merge LspConfig.Env after tool.Env.
	env := map[string]string{}
	for k, v := range opts.Tool.Env {
		env[k] = v
	}
	for _, k := range opts.Tool.PassEnv {
		if v, ok := os.LookupEnv(k); ok {
			env[k] = v
		}
	}
	env["TERM"] = "dumb"
	for k, v := range lspCfg.Env {
		env[k] = v
	}
	if opts.ProjectConfig != nil {
		for _, k := range opts.ProjectConfig.PassEnv {
			if v, ok := os.LookupEnv(k); ok {
				env[k] = v
			}
		}
		if o, ok := opts.ProjectConfig.Overrides[opts.ToolName]; ok {
			for k, v := range o.Env {
				env[k] = v
			}
		}
	}
	envArr := make([]string, 0, len(env))
	for k, v := range env {
		envArr = append(envArr, k+"="+v)
	}

	// Mounts: workspace + tool caches + lsp caches + dep caches + pass_files.
	caches := append([]config.CacheMount(nil), opts.Tool.Cache...)
	caches = append(caches, lspCfg.Cache...)
	if len(opts.Tool.Requires) > 0 {
		global, _ := config.LoadGlobalConfig()
		for _, dep := range opts.Tool.Requires {
			if global != nil {
				if t, ok := global.Tools[dep]; ok {
					caches = append(caches, t.Cache...)
					continue
				}
			}
			if t, ok, _ := tools.Lookup(dep, ""); ok {
				caches = append(caches, t.Cache...)
			}
		}
	}
	mounts, err := buildMounts(opts.Tool, opts.ProjectDir, caches, opts.ProjectConfig)
	if err != nil {
		return -1, err
	}

	// Pipes: host side keeps fd for writing inbound / reading outbound; container keeps the other ends.
	toContRead, toContWrite, err := pipePair()
	if err != nil {
		return -1, errs.Runtimef("pipe: %v", err)
	}
	fromContRead, fromContWrite, err := pipePair()
	if err != nil {
		return -1, errs.Runtimef("pipe: %v", err)
	}

	cfg := bridge.DefaultContainerConfig()
	cfg.CPUs = opts.Tool.CPUs
	cfg.MemoryBytes = opts.Tool.MemoryMB * 1024 * 1024
	cfg.Arguments = append([]string(nil), lspCfg.Command...)
	cfg.WorkingDirectory = opts.Tool.Workdir
	cfg.EnvVars = envArr
	cfg.Mounts = mounts
	cfg.StdinFD = int32(toContRead.Fd())
	cfg.StdoutFD = int32(fromContWrite.Fd())
	cfg.StderrFD = 2
	cfg.UseTerminal = false
	cfg.EnableNetworking = needsNet
	if needsNet {
		cfg.DNSNameservers = []string{"1.1.1.1", "8.8.8.8"}
		cfg.AutoInjectHost = true
	}

	rootfsSize := opts.Tool.RootfsSizeMB * 1024 * 1024
	ctr, err := r.acquireContainerForLSP(mgr, id, imageRef, rootfsSize, cfg, opts)
	if err != nil {
		return -1, err
	}
	defer ctr.Close()

	if err := ctr.Create(); err != nil {
		return -1, errs.Containerf("create: %v", err)
	}
	if err := ctr.Start(); err != nil {
		return -1, errs.Containerf("start: %v", err)
	}

	// The Swift bridge relays host<->VM stdio over vsock: it keeps reading
	// from toContRead and writing to fromContWrite for the entire lifetime
	// of the container. Closing those ends on the host side here would cut
	// the relay and the LSP process would see an immediate stdin EOF and
	// exit 1 with no output. We keep them open and let `defer ctr.Close()`
	// plus the explicit closes at the bottom of the function tear things
	// down after the process has exited.

	guestRoot := opts.Tool.Workdir
	if guestRoot == "" {
		guestRoot = "/workspace"
	}
	proxy := lsp.NewProxy(opts.ProjectDir, guestRoot)

	// Three concurrent actors: inbound proxy, outbound proxy, wait(). Any one
	// finishing triggers cleanup of the others and we return the exit code.
	var exit int32
	var once sync.Once
	done := make(chan struct{})
	setExit := func(code int32) {
		once.Do(func() { exit = code; close(done) })
	}

	go func() {
		reader := lsp.NewFrameReader(os.Stdin)
		writer := lsp.NewFrameWriter(toContWrite)
		for {
			msg, err := reader.ReadMessage()
			if err != nil || msg == nil {
				_ = toContWrite.Close()
				return
			}
			if err := writer.WriteMessage(proxy.RewriteInbound(msg)); err != nil {
				return
			}
		}
	}()

	go func() {
		reader := lsp.NewFrameReader(fromContRead)
		writer := lsp.NewFrameWriter(os.Stdout)
		for {
			msg, err := reader.ReadMessage()
			if err != nil || msg == nil {
				return
			}
			if err := writer.WriteMessage(proxy.RewriteOutbound(msg)); err != nil {
				return
			}
		}
	}()

	go func() {
		code, _ := ctr.Wait()
		setExit(code)
	}()

	<-done

	_ = ctr.Stop()
	mgr.Delete(id)
	_ = toContRead.Close()
	_ = toContWrite.Close()
	_ = fromContRead.Close()
	_ = fromContWrite.Close()
	return exit, nil
}

// acquireContainerForLSP mirrors the 4-tier cascade but without the project-dir
// fallback rules that only apply to RunEphemeral.
func (r *ephemeralRunner) acquireContainerForLSP(
	mgr *bridge.Manager,
	id, imageRef string,
	rootfsSize uint64,
	cfg bridge.ContainerConfig,
	opts RunLSPOptions,
) (*bridge.Container, error) {
	if opts.ProjectRoot != "" {
		explicitID := ""
		if opts.ProjectConfig != nil {
			explicitID = opts.ProjectConfig.ProjectID
		}
		if p := runtime.ResolveProjectRootfs(opts.ProjectRoot, opts.ToolName, explicitID); p != "" {
			ctr, err := r.tryCachedRootfs(mgr, id, imageRef, p, cfg)
			if err == nil {
				return ctr, nil
			}
			fmt.Fprintf(os.Stderr, "warning: project rootfs %s failed to load: %v; falling back\n", p, err)
		}
	}
	if opts.Tool.BuildRootfs != "" {
		if _, statErr := os.Stat(opts.Tool.BuildRootfs); statErr == nil {
			ctr, err := r.tryCachedRootfs(mgr, id, imageRef, opts.Tool.BuildRootfs, cfg)
			if err == nil {
				return ctr, nil
			}
			fmt.Fprintf(os.Stderr, "warning: buildRootfs %s failed to load: %v; falling back\n", opts.Tool.BuildRootfs, err)
		}
	}
	if ctr := r.tryRootfsCacheHit(mgr, id, imageRef, rootfsSize, cfg); ctr != nil {
		return ctr, nil
	}
	return r.createOrRetry(mgr, id, imageRef, rootfsSize, cfg)
}

// applyLspResourceFloors lifts a tool definition's resource limits to the
// minimums a language server usually needs. Applied per-call; the stored
// definition is not modified, so `silo run` keeps its lighter defaults.
func applyLspResourceFloors(t *config.ToolDefinition) {
	const (
		minLspCPUs     int32  = 2
		minLspMemoryMB uint64 = 2048
	)
	if t.CPUs < minLspCPUs {
		t.CPUs = minLspCPUs
	}
	if t.MemoryMB < minLspMemoryMB {
		t.MemoryMB = minLspMemoryMB
	}
}

// warnIfLspBakeMissing prints a best-effort hint when the LSP is about to
// boot against a rootfs that won't contain the language server. Covers two
// cases:
//
//  1. The tool declares `lsp.install` but has no BuildRootfs — either the
//     install predates the sync-time bake wiring, or the user has never
//     run install/sync. Suggest `silo install --force`.
//
//  2. The project pins a different image than the tool's baseline and no
//     project rootfs exists. Suggest `silo sync`, which bakes the LSP
//     against the pinned toolchain so the server matches `silo run`.
//
// Both are hints, not errors — the LSP may still boot from a fallback
// rootfs if the server happens to be in the base image.
func warnIfLspBakeMissing(opts RunLSPOptions, imageRef string) {
	if opts.Tool.LSP == nil {
		return
	}
	if opts.Tool.LSP.Install != "" && opts.Tool.BuildRootfs == "" {
		fmt.Fprintf(os.Stderr,
			"hint: %s has an LSP install step but no baked rootfs; "+
				"run `silo install %s --force` to bake it.\n",
			opts.ToolName, opts.ToolName)
	}
	if imageRef != "" && imageRef != opts.Tool.Image && opts.ProjectRoot != "" {
		explicitID := ""
		if opts.ProjectConfig != nil {
			explicitID = opts.ProjectConfig.ProjectID
		}
		if runtime.ResolveProjectRootfs(opts.ProjectRoot, opts.ToolName, explicitID) == "" {
			fmt.Fprintf(os.Stderr,
				"hint: project pins %s but no project rootfs exists; "+
					"run `silo sync` so the LSP matches the pinned version.\n",
				imageRef)
		}
	}
}

// pipePair creates an OS pipe; read end in [0], write end in [1].
func pipePair() (*os.File, *os.File, error) {
	var fds [2]int
	if err := syscall.Pipe(fds[:]); err != nil {
		return nil, nil, err
	}
	r := os.NewFile(uintptr(fds[0]), "lsp-pipe-read")
	w := os.NewFile(uintptr(fds[1]), "lsp-pipe-write")
	return r, w, nil
}

// unused import silencer (sync always used above; keep strings+filepath just in case)
var (
	_ = strings.Contains
	_ = filepath.Join
)
