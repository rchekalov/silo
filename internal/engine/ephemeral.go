// SPDX-License-Identifier: Apache-2.0

package engine

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

	"github.com/schollz/progressbar/v3"
	"golang.org/x/sys/unix"
	"golang.org/x/term"

	"github.com/rchekalov/silo/internal/bridge"
	"github.com/rchekalov/silo/internal/cache"
	"github.com/rchekalov/silo/internal/config"
	"github.com/rchekalov/silo/internal/errs"
	"github.com/rchekalov/silo/internal/network"
	"github.com/rchekalov/silo/internal/runtime"
	"github.com/rchekalov/silo/internal/tools"
)

// ephemeralRunner is the mechanical backend used by ContainerEngine. It owns
// the four-tier rootfs cascade and the lifecycle for one VM invocation.
type ephemeralRunner struct {
	kernelPath string
	initfsPath string
	rootPath   string
}

func newEphemeralRunner(kernel, initfs, root string) *ephemeralRunner {
	return &ephemeralRunner{kernelPath: kernel, initfsPath: initfs, rootPath: root}
}

func (r *ephemeralRunner) newManager(networking bool) (*bridge.Manager, error) {
	m, err := bridge.NewManager(r.kernelPath, r.initfsPath, r.rootPath, networking)
	if err != nil {
		return nil, errs.Containerf("%v", err)
	}
	return m, nil
}

// PullImage pulls and optionally caches a rootfs for `cacheFor`.
func (r *ephemeralRunner) PullImage(reference string, cacheFor *config.ToolDefinition) error {
	mgr, err := r.newManager(false)
	if err != nil {
		return err
	}
	defer mgr.Close()
	id := "pull-" + shortID()

	var rootfsSize uint64 = 512 * 1024 * 1024
	if cacheFor != nil {
		rootfsSize = cacheFor.RootfsSizeMB * 1024 * 1024
	}
	cfg := bridge.DefaultContainerConfig()
	cfg.Arguments = []string{"/bin/true"}

	// Apple Containerization doesn't feed progress through our bridge, so
	// we sample the two directories bytes land in (~/.silo/content for OCI
	// blobs, and the container-specific rootfs.ext4 for unpack progress)
	// and feed the deltas into schollz/progressbar. One writer, one spinner.
	contentDir := runtime.ContentStore()
	containerDir := filepath.Join(r.rootPath, "containers", id)
	baseline := duBytes(contentDir)

	// Probe the local content store. If the image is already there, the
	// pull is a no-op and the visible work is unpacking from cached blobs
	// into the new container's rootfs — so "0 B downloaded" would be true
	// but misleading. Render a single-clause label instead.
	cached := false
	if img, err := mgr.ImageGet(reference, false); err == nil {
		cached = true
		img.Close()
	}
	verb := "Pulling"
	if cached {
		verb = "Unpacking"
	}

	bar := progressbar.NewOptions(-1,
		progressbar.OptionSetWriter(os.Stderr),
		progressbar.OptionSetDescription(verb+" "+reference),
		progressbar.OptionSpinnerType(14),
		progressbar.OptionThrottle(100*time.Millisecond),
		progressbar.OptionClearOnFinish(),
	)
	stopBar := make(chan struct{})
	go func() {
		spin := time.NewTicker(100 * time.Millisecond)
		defer spin.Stop()
		var i int
		for {
			select {
			case <-stopBar:
				return
			case <-spin.C:
				if i%5 == 0 { // resample bytes every ~500ms, cheap
					imgDelta := duBytes(contentDir) - baseline
					if imgDelta < 0 {
						imgDelta = 0
					}
					ctrNow := duBytes(containerDir)
					// Tag-drift safety net: if we started on "cached" but
					// new blobs land (mutable tag moved remotely), swap
					// back to the honest two-clause label.
					const driftThreshold = 1 << 20 // 1 MiB
					if cached && imgDelta > driftThreshold {
						cached = false
					}
					if cached {
						bar.Describe(fmt.Sprintf(
							"Unpacking %s — %s",
							reference, humanBytes(ctrNow),
						))
					} else {
						bar.Describe(fmt.Sprintf(
							"Pulling %s — %s downloaded, %s unpacked",
							reference, humanBytes(imgDelta), humanBytes(ctrNow),
						))
					}
				}
				_ = bar.Add(1)
				i++
			}
		}
	}()

	ctr, err := mgr.CreateContainerFromRef(id, reference, rootfsSize, cfg)
	close(stopBar)
	_ = bar.Finish()
	if err != nil {
		return errs.Containerf("pull create: %v", err)
	}
	defer ctr.Close()

	if cacheFor != nil {
		if img, err := mgr.ImageGet(reference, false); err == nil {
			rootfs := filepath.Join(r.rootPath, "containers", id, "rootfs.ext4")
			if _, err := os.Stat(rootfs); err == nil {
				_ = cache.NewRootfs("").Store(rootfs, img.Digest(), rootfsSize)
			}
			img.Close()
		}
	}
	mgr.Delete(id)
	return nil
}

// Run executes a command in an ephemeral VM. See ContainerEngine.RunEphemeral.
func (r *ephemeralRunner) Run(opts RunEphemeralOptions) (int32, error) {
	maintenanceBeforeRun()
	id := fmt.Sprintf("silo-%s-%s", opts.ToolName, shortID())

	applyResourceOverrides(&opts.Tool, opts.ToolName, opts.ProjectConfig)
	effectiveNet, effectivePorts, imageRef := resolveOverrides(opts.Tool, opts.ToolName, opts.ProjectConfig)
	hasPorts := len(effectivePorts) > 0
	// Networking turns on for tools that opted into hostAccess in registry/overrides
	// or that publish ports. Whenever networking is on, the proxy is started
	// unconditionally — empty allowlist means deny-everything (default-deny
	// contract). To intentionally allow open internet, set allow:["*"].
	needsNet := (effectiveNet != nil && effectiveNet.HostAccess) || hasPorts

	mgr, err := r.newManager(needsNet)
	if err != nil {
		return -1, err
	}
	defer mgr.Close()

	var proxy *network.HTTPProxy
	if needsNet {
		rule := runtimeProxyRule(effectiveNet)
		proxy, err = network.StartHTTPProxy(rule)
		if err != nil {
			return -1, errs.Containerf("start network proxy: %v", err)
		}
		defer proxy.Stop()
	}

	env := buildEnv(opts.Tool, opts.ToolName, opts.ProjectConfig)
	env = applyVenvAutoActivate(env, opts.ToolName, opts.ProjectDir, opts.Tool.Workdir)
	if proxy != nil {
		proxyURL := fmt.Sprintf("http://host.silo.internal:%d", proxy.Port())
		env = appendEnv(env, "HTTP_PROXY", proxyURL)
		env = appendEnv(env, "HTTPS_PROXY", proxyURL)
		env = appendEnv(env, "http_proxy", proxyURL)
		env = appendEnv(env, "https_proxy", proxyURL)
		env = appendEnv(env, "NO_PROXY", "localhost,127.0.0.1")
	}
	hostSshSock, sshAgentOn := sshAgentSocket(opts.Tool)
	if sshAgentOn {
		env = appendEnv(env, "SSH_AUTH_SOCK", sshAgentGuestPath)
	}
	effectiveCache := mergeCacheMounts(opts.Tool)

	mounts, err := buildMounts(opts.Tool, opts.ProjectDir, effectiveCache, opts.ProjectConfig)
	if err != nil {
		return -1, err
	}

	captureStdout := opts.Stdout != nil
	isTTY := opts.Interactive && !captureStdout && term.IsTerminal(int(os.Stdin.Fd()))

	// If capturing stdout, set up a pipe to forward container stdout into opts.Stdout.
	var capturePipeRead, capturePipeWrite *os.File
	if captureStdout {
		capturePipeRead, capturePipeWrite, err = pipePair()
		if err != nil {
			return -1, errs.Runtimef("stdout pipe: %v", err)
		}
	}

	cfg := bridge.DefaultContainerConfig()
	cfg.CPUs = opts.Tool.CPUs
	cfg.MemoryBytes = opts.Tool.MemoryMB * 1024 * 1024
	cfg.Arguments = append(strings.Fields(opts.Command), opts.Arguments...)
	cfg.WorkingDirectory = opts.Tool.Workdir
	cfg.EnvVars = env
	cfg.Mounts = mounts
	if sshAgentOn {
		cfg.Sockets = []bridge.SocketSpec{{
			HostSource:       hostSshSock,
			GuestDestination: sshAgentGuestPath,
		}}
	}
	switch {
	case captureStdout:
		cfg.StdoutFD = int32(capturePipeWrite.Fd())
		cfg.StderrFD = 2
	case isTTY:
		cfg.StdoutFD, cfg.StderrFD = -1, -1
		cfg.UseTerminal = true
	default:
		cfg.StdoutFD, cfg.StderrFD = 1, 2
	}
	cfg.EnableNetworking = needsNet
	if needsNet {
		cfg.DNSNameservers = []string{"1.1.1.1", "8.8.8.8"}
		cfg.AutoInjectHost = true
	}

	rootfsSize := opts.Tool.RootfsSizeMB * 1024 * 1024
	ctr, err := r.acquireContainer(mgr, id, imageRef, rootfsSize, cfg, &opts)
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

	// Close the container-owned write end on the host; drain the read end
	// into opts.Stdout. captureDone closes when the pipe EOFs.
	var captureDone chan struct{}
	if captureStdout {
		_ = capturePipeWrite.Close()
		captureDone = make(chan struct{})
		go func() {
			_, _ = io.Copy(opts.Stdout, capturePipeRead)
			close(captureDone)
		}()
	}

	var pf *network.PortForwarder
	if hasPorts {
		vmIP := ctr.VMIP()
		if vmIP == "" {
			return -1, errs.Containerf("port forwarding requires a VM IP but none was assigned")
		}
		pf, err = network.StartPortForwarder(effectivePorts, vmIP)
		if err != nil {
			return -1, errs.Containerf("port forwarding: %v", err)
		}
	}

	if isTTY {
		enableISIG()
		if cols, rows, err := bridge.TerminalSize(); err == nil {
			_ = ctr.Resize(cols, rows)
		}
	}

	// First Ctrl+C → graceful container stop; second → force exit.
	cancelSignals := HandleInterrupts(
		func() { _ = ctr.Stop() },
		func() { os.Exit(130) },
	)

	exit, waitErr := ctr.Wait()
	cancelSignals()

	// Flush the guest page cache to the host virtio-fs share before teardown.
	// Without this, writes the user's command made to /workspace may not have
	// completed writeback before mgr.Delete() unmounts the share — visible as
	// `pip install` "succeeding" but leaving no files on the host venv.
	flushGuestSync(ctr)

	_ = ctr.Stop()
	mgr.Delete(id)
	if pf != nil {
		pf.Stop()
	}
	// With the container gone, the write end of the capture pipe has been closed
	// by the bridge; draining now completes quickly.
	if captureStdout {
		_ = capturePipeRead.Close()
		<-captureDone
	}
	if isTTY {
		resetTerminal()
	}

	if waitErr != nil {
		return exit, errs.Containerf("wait: %v", waitErr)
	}
	return exit, nil
}

// RunSetup runs the setup command in a networked VM and persists the rootfs on success.
//
// Setup has higher resource floors than regular runs because it typically
// executes `apt-get`/`npm i -g` which need more headroom:
//   - rootfs size ≥ 4 GB (below this, package install fills the disk)
//   - CPUs ≥ 4, memory ≥ 4 GB
//
// These match the Swift ImageBuilder defaults on main. We bump the in-memory
// ToolDefinition for this call only — the stored config is untouched.
func (r *ephemeralRunner) RunSetup(opts RunSetupOptions) (int32, error) {
	id := fmt.Sprintf("silo-setup-%s-%s", opts.ToolName, shortID())

	applyResourceOverrides(&opts.Tool, opts.ToolName, opts.ProjectConfig)
	applySetupResourceFloors(&opts.Tool)

	mgr, err := r.newManager(true)
	if err != nil {
		return -1, err
	}
	defer mgr.Close()

	effectiveNet, effectivePorts, imageRef := resolveOverrides(opts.Tool, opts.ToolName, opts.ProjectConfig)
	hasPorts := len(effectivePorts) > 0

	// Setup runs apt-get / npm install -g / pip install. Networking is always
	// on for the build stage, but every byte still flows through the proxy
	// allowlist — the union of runtime allow + installAllow (apt repos and
	// other one-shot bake-time origins). Empty rule means deny-everything;
	// the build will fail loudly rather than silently reach the open internet.
	var proxy *network.HTTPProxy
	rule := setupProxyRule(effectiveNet)
	proxy, err = network.StartHTTPProxy(rule)
	if err != nil {
		return -1, errs.Containerf("start network proxy: %v", err)
	}
	defer proxy.Stop()

	env := buildEnv(opts.Tool, opts.ToolName, opts.ProjectConfig)
	if proxy != nil {
		proxyURL := fmt.Sprintf("http://host.silo.internal:%d", proxy.Port())
		env = appendEnv(env, "HTTP_PROXY", proxyURL)
		env = appendEnv(env, "HTTPS_PROXY", proxyURL)
		env = appendEnv(env, "http_proxy", proxyURL)
		env = appendEnv(env, "https_proxy", proxyURL)
		env = appendEnv(env, "NO_PROXY", "localhost,127.0.0.1")
	}
	hostSshSock, sshAgentOn := sshAgentSocket(opts.Tool)
	if sshAgentOn {
		env = appendEnv(env, "SSH_AUTH_SOCK", sshAgentGuestPath)
	}
	effectiveCache := mergeCacheMounts(opts.Tool)
	mounts, err := buildMounts(opts.Tool, opts.ProjectDir, effectiveCache, opts.ProjectConfig)
	if err != nil {
		return -1, err
	}
	isTTY := term.IsTerminal(int(os.Stdin.Fd()))

	cfg := bridge.DefaultContainerConfig()
	cfg.CPUs = opts.Tool.CPUs
	cfg.MemoryBytes = opts.Tool.MemoryMB * 1024 * 1024
	cfg.Arguments = append(strings.Fields(opts.Command), opts.Arguments...)
	cfg.WorkingDirectory = opts.Tool.Workdir
	cfg.EnvVars = env
	cfg.Mounts = mounts
	if sshAgentOn {
		cfg.Sockets = []bridge.SocketSpec{{
			HostSource:       hostSshSock,
			GuestDestination: sshAgentGuestPath,
		}}
	}
	if isTTY {
		cfg.StdoutFD, cfg.StderrFD = -1, -1
		cfg.UseTerminal = true
	} else {
		cfg.StdoutFD, cfg.StderrFD = 1, 2
	}
	cfg.EnableNetworking = true
	cfg.DNSNameservers = []string{"1.1.1.1", "8.8.8.8"}
	cfg.AutoInjectHost = true

	rootfsSize := opts.Tool.RootfsSizeMB * 1024 * 1024

	var ctr *bridge.Container
	if !opts.Global {
		globalRootfs := runtime.GlobalBuildRootfs(opts.ToolName)
		if _, err := os.Stat(globalRootfs); err == nil {
			var loadErr error
			ctr, loadErr = r.tryCachedRootfs(mgr, id, imageRef, globalRootfs, cfg)
			if loadErr != nil {
				fmt.Fprintf(os.Stderr, "warning: buildRootfs %s failed to load: %v; falling back\n", globalRootfs, loadErr)
			}
		}
	}
	if ctr == nil {
		ctr = r.tryRootfsCacheHit(mgr, id, imageRef, rootfsSize, cfg)
	}
	if ctr == nil {
		ctr, err = r.createOrRetry(mgr, id, imageRef, rootfsSize, cfg)
		if err != nil {
			return -1, err
		}
	}
	defer ctr.Close()

	if err := ctr.Create(); err != nil {
		return -1, errs.Containerf("create: %v", err)
	}
	if err := ctr.Start(); err != nil {
		return -1, errs.Containerf("start: %v", err)
	}

	var pf *network.PortForwarder
	if hasPorts {
		if vmIP := ctr.VMIP(); vmIP != "" {
			pf, _ = network.StartPortForwarder(effectivePorts, vmIP)
		}
	}

	if isTTY {
		enableISIG()
		if cols, rows, err := bridge.TerminalSize(); err == nil {
			_ = ctr.Resize(cols, rows)
		}
	}

	cancelSignals := HandleInterrupts(
		func() { _ = ctr.Stop() },
		func() { os.Exit(130) },
	)

	exit, waitErr := ctr.Wait()
	cancelSignals()

	// Belt-and-braces: even with the `&& sync` wrap silo build prepends, exec
	// a final `sync` while the container is still alive so the ext4 block
	// device is fully flushed before copyFile snapshots it.
	flushGuestSync(ctr)

	// Persist rootfs on success BEFORE stopping the container — once the VM
	// exits, the rootfs file is still present until Delete is called.
	containerDir := filepath.Join(r.rootPath, "containers", id)
	if exit == 0 && waitErr == nil {
		srcRootfs := filepath.Join(containerDir, "rootfs.ext4")
		if _, err := os.Stat(srcRootfs); err != nil {
			return exit, errs.Runtimef("container rootfs not found after execution")
		}
		if err := os.MkdirAll(filepath.Dir(opts.TargetRootfs), 0o755); err != nil {
			return exit, errs.Runtimef("create parent dir: %v", err)
		}
		if err := copyFile(srcRootfs, opts.TargetRootfs); err != nil {
			return exit, errs.Runtimef("save rootfs to %s: %v", opts.TargetRootfs, err)
		}
		fmt.Fprintf(os.Stderr, "Setup complete. Rootfs saved to %s\n", opts.TargetRootfs)
	} else {
		// Setup failed — don't leak a partial rootfs in ~/.silo/containers/.
		// The VM-side filesystem state is unusable once the setup script failed
		// mid-way, so there's no value in keeping it. Log and continue with
		// the normal cleanup path so the caller can see the original exit code.
		fmt.Fprintf(os.Stderr, "Setup failed (exit %d); discarding partial rootfs.\n", exit)
		_ = os.RemoveAll(containerDir)
	}

	_ = ctr.Stop()
	mgr.Delete(id)
	if pf != nil {
		pf.Stop()
	}
	if isTTY {
		resetTerminal()
	}

	if waitErr != nil {
		return exit, errs.Containerf("wait: %v", waitErr)
	}
	return exit, nil
}

// acquireContainer implements the 4-tier rootfs cascade.
func (r *ephemeralRunner) acquireContainer(
	mgr *bridge.Manager,
	id, imageRef string,
	rootfsSize uint64,
	cfg bridge.ContainerConfig,
	opts *RunEphemeralOptions,
) (*bridge.Container, error) {
	// 1. Project-local rootfs (auto-bake under ~/.silo/baked/<hash>/ resolved
	// via the project meta, or `silo build` output at <project>/.silo/<tool>/).
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
	// 2. Global build rootfs
	if opts.Tool.BuildRootfs != "" {
		if _, statErr := os.Stat(opts.Tool.BuildRootfs); statErr == nil {
			ctr, err := r.tryCachedRootfs(mgr, id, imageRef, opts.Tool.BuildRootfs, cfg)
			if err == nil {
				return ctr, nil
			}
			fmt.Fprintf(os.Stderr, "warning: buildRootfs %s failed to load: %v; falling back\n", opts.Tool.BuildRootfs, err)
		}
	}
	// 3. Rootfs cache
	if ctr := r.tryRootfsCacheHit(mgr, id, imageRef, rootfsSize, cfg); ctr != nil {
		return ctr, nil
	}
	// 4. Full OCI unpack
	return r.createOrRetry(mgr, id, imageRef, rootfsSize, cfg)
}

func (r *ephemeralRunner) tryCachedRootfs(
	mgr *bridge.Manager, id, imageRef, rootfsSource string, cfg bridge.ContainerConfig,
) (*bridge.Container, error) {
	containerDir := filepath.Join(r.rootPath, "containers", id)
	if err := os.MkdirAll(containerDir, 0o755); err != nil {
		return nil, err
	}
	cloned := filepath.Join(containerDir, "rootfs.ext4")
	if err := cache.CloneOrCopyFile(rootfsSource, cloned); err != nil {
		_ = os.RemoveAll(containerDir)
		return nil, err
	}
	// Image must already be in the local content store — a persisted rootfs
	// was produced by pulling the same reference at install/setup time.
	img, err := mgr.ImageGet(imageRef, false)
	if err != nil {
		_ = os.RemoveAll(containerDir)
		return nil, err
	}
	defer img.Close()
	ctr, err := mgr.CreateContainerFromImage(id, img, bridge.Block(cloned, "/"), cfg)
	if err != nil {
		_ = os.RemoveAll(containerDir)
		return nil, err
	}
	return ctr, nil
}

func (r *ephemeralRunner) tryRootfsCacheHit(
	mgr *bridge.Manager, id, imageRef string, rootfsSize uint64, cfg bridge.ContainerConfig,
) *bridge.Container {
	img, err := mgr.ImageGet(imageRef, false)
	if err != nil {
		return nil
	}
	defer img.Close()
	digest := img.Digest()
	c := cache.NewRootfs("")
	if !c.Has(digest, rootfsSize) {
		return nil
	}
	containerDir := filepath.Join(r.rootPath, "containers", id)
	if err := os.MkdirAll(containerDir, 0o755); err != nil {
		return nil
	}
	cloned := filepath.Join(containerDir, "rootfs.ext4")
	if err := c.CloneTo(cloned, digest, rootfsSize); err != nil {
		return nil
	}
	// Re-acquire image handle (previous one is used by ImageGet under-the-hood).
	img2, err := mgr.ImageGet(imageRef, false)
	if err != nil {
		_ = os.RemoveAll(containerDir)
		return nil
	}
	defer img2.Close()
	ctr, err := mgr.CreateContainerFromImage(id, img2, bridge.Block(cloned, "/"), cfg)
	if err != nil {
		_ = os.RemoveAll(containerDir)
		return nil
	}
	return ctr
}

func (r *ephemeralRunner) createOrRetry(
	mgr *bridge.Manager, id, imageRef string, rootfsSize uint64, cfg bridge.ContainerConfig,
) (*bridge.Container, error) {
	ctr, err := mgr.CreateContainerFromRef(id, imageRef, rootfsSize, cfg)
	if err != nil {
		// Cleanup container dir then re-pull and retry once.
		_ = os.RemoveAll(filepath.Join(r.rootPath, "containers", id))
		if perr := mgr.ImagePull(imageRef); perr != nil {
			return nil, errs.Containerf("%v", perr)
		}
		ctr, err = mgr.CreateContainerFromRef(id, imageRef, rootfsSize, cfg)
		if err != nil {
			return nil, errs.Containerf("%v", err)
		}
	}
	// Store freshly unpacked rootfs in cache.
	if img, err := mgr.ImageGet(imageRef, false); err == nil {
		rootfs := filepath.Join(r.rootPath, "containers", id, "rootfs.ext4")
		if _, err := os.Stat(rootfs); err == nil {
			_ = cache.NewRootfs("").Store(rootfs, img.Digest(), rootfsSize)
		}
		img.Close()
	}
	return ctr, nil
}

// --- helpers ----------------------------------------------------------------

// applyResourceOverrides applies project-level CPU/memory/rootfs/workdir/
// passEnv/lsp overrides onto the local ToolDefinition copy. The stored
// definition is unaffected because engine options are passed by value.
//
// This deliberately delegates to config.ApplyOverride for the heavyweight
// merge logic (LSP env-merge, passEnv dedup, etc.) but only copies the
// resource-shaped fields back. We avoid swapping the whole ToolDefinition
// because the engine has older code paths that read other fields (env, ports,
// network, image, cache) via a separate override-resolution path with subtly
// different merge semantics, and reusing those is intentional.
func applyResourceOverrides(tool *config.ToolDefinition, name string, pc *config.ProjectConfig) {
	if pc == nil {
		return
	}
	// Project-wide passSshAgent applies to every tool; per-tool override below
	// can OR it on but never force off.
	if pc.PassSshAgent {
		tool.PassSshAgent = true
	}
	o, ok := pc.Overrides[name]
	if !ok {
		return
	}
	if o.PassSshAgent {
		tool.PassSshAgent = true
	}
	if o.CPUs != 0 {
		tool.CPUs = o.CPUs
	}
	if o.MemoryMB != 0 {
		tool.MemoryMB = o.MemoryMB
	}
	if o.RootfsSizeMB != 0 {
		tool.RootfsSizeMB = o.RootfsSizeMB
	}
	if o.Workdir != "" {
		tool.Workdir = o.Workdir
	}
	if len(o.PassEnv) > 0 {
		// Append override passEnv to the base, deduping while preserving order.
		// buildEnv reads tool.PassEnv when materializing the env map, so this
		// is the single hook that makes per-tool passEnv work.
		seen := make(map[string]struct{}, len(tool.PassEnv)+len(o.PassEnv))
		merged := make([]string, 0, len(tool.PassEnv)+len(o.PassEnv))
		for _, k := range tool.PassEnv {
			if _, ok := seen[k]; ok {
				continue
			}
			seen[k] = struct{}{}
			merged = append(merged, k)
		}
		for _, k := range o.PassEnv {
			if _, ok := seen[k]; ok {
				continue
			}
			seen[k] = struct{}{}
			merged = append(merged, k)
		}
		tool.PassEnv = merged
	}
	if o.LSP != nil {
		// Reuse config-package merge so semantics match silo current / silo sync.
		merged := config.ApplyOverride(*tool, config.ToolOverride{LSP: o.LSP})
		tool.LSP = merged.LSP
	}
}

func resolveOverrides(tool config.ToolDefinition, name string, pc *config.ProjectConfig) (
	effectiveNet *config.NetworkConfig,
	effectivePorts []config.PortMapping,
	imageRef string,
) {
	effectiveNet = tool.Network
	effectivePorts = tool.Ports
	imageRef = tool.Image
	if pc == nil {
		return effectiveNet, effectivePorts, imageRef
	}
	o, ok := pc.Overrides[name]
	if !ok {
		return effectiveNet, effectivePorts, imageRef
	}
	if o.Network != nil {
		// Per-field merge so a project that adds `corp.repo` to the allowlist
		// keeps the registry's package-manager origins (pypi, npm, etc.).
		// Without this the wholesale replace silently strips the registry
		// allowlist and breaks `pip install` / `npm install` for any project
		// that customized network at all.
		effectiveNet = mergeNetwork(effectiveNet, o.Network)
	}
	if o.Ports != nil {
		effectivePorts = o.Ports
	}
	if o.Image != "" {
		imageRef = o.Image
	}
	return effectiveNet, effectivePorts, imageRef
}

// mergeNetwork is a thin wrapper around the package-private merge in config
// so the engine doesn't need to reach into config internals. Identical
// semantics to ApplyOverride's network merge.
func mergeNetwork(base, overlay *config.NetworkConfig) *config.NetworkConfig {
	// Reuse ApplyOverride's per-field merge. Build a minimal ToolOverride
	// holding only Network so unrelated fields don't perturb the result.
	merged := config.ApplyOverride(
		config.ToolDefinition{Network: base},
		config.ToolOverride{Network: overlay},
	)
	return merged.Network
}

// runtimeProxyRule is the proxy rule applied during `silo run`. It's the
// runtime allowlist — installAllow is intentionally excluded so apt repos
// and other bake-time origins aren't reachable at runtime.
func runtimeProxyRule(net *config.NetworkConfig) config.ProxyConfig {
	if net == nil || net.Proxy == nil {
		return config.ProxyConfig{}
	}
	return config.ProxyConfig{
		Allow: append([]string(nil), net.Proxy.Allow...),
		Deny:  append([]string(nil), net.Proxy.Deny...),
	}
}

// setupProxyRule is the proxy rule applied during `silo build` / `silo install`
// postInstall / `silo add`. Allow is the union of runtime allow + installAllow
// so apt repos and other one-shot bake-time origins are reachable for the
// duration of the build stage. installAllow is dropped from the runtime path
// (see runtimeProxyRule).
func setupProxyRule(net *config.NetworkConfig) config.ProxyConfig {
	if net == nil || net.Proxy == nil {
		return config.ProxyConfig{}
	}
	allow := make([]string, 0, len(net.Proxy.Allow)+len(net.Proxy.InstallAllow))
	allow = append(allow, net.Proxy.Allow...)
	allow = append(allow, net.Proxy.InstallAllow...)
	return config.ProxyConfig{
		Allow: allow,
		Deny:  append([]string(nil), net.Proxy.Deny...),
	}
}

func buildEnv(tool config.ToolDefinition, toolName string, pc *config.ProjectConfig) []string {
	env := map[string]string{}
	for k, v := range tool.Env {
		env[k] = v
	}
	for _, key := range tool.PassEnv {
		if v, ok := os.LookupEnv(key); ok {
			env[key] = v
		}
	}
	if _, ok := env["TERM"]; !ok {
		if host := os.Getenv("TERM"); host != "" {
			env["TERM"] = host
		} else {
			env["TERM"] = "xterm-256color"
		}
	}
	if pc != nil {
		for _, key := range pc.PassEnv {
			if v, ok := os.LookupEnv(key); ok {
				env[key] = v
			}
		}
		if o, ok := pc.Overrides[toolName]; ok {
			for k, v := range o.Env {
				env[k] = v
			}
		}
	}
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

// applyVenvAutoActivate mirrors `source venv/bin/activate` when the project
// root has a Python venv. Without this, `silo run python venv/bin/pip install …`
// invokes the rootfs python (sys.executable=/usr/local/bin/python), which
// resolves pip from the rootfs site-packages and installs into /usr/local —
// thrown away when the ephemeral container exits. By injecting VIRTUAL_ENV +
// prepending the venv's bin dir to PATH the host venv "just works" for pip /
// python / pytest the way it would inside a normal activated shell.
//
// Scoped to the python tool so a stray venv/ in a node project doesn't
// surprise other tools' env. `.venv` and `venv` are the conventions; first
// match wins.
func applyVenvAutoActivate(env []string, toolName, projectDir, workdir string) []string {
	if toolName != "python" || projectDir == "" || workdir == "" {
		return env
	}
	for _, candidate := range []string{".venv", "venv"} {
		// Lstat, not Stat: venv/bin/python is a symlink whose target lives in
		// the guest rootfs (e.g. /usr/local/bin/python) and won't resolve on
		// the host. We just need the symlink to exist.
		if _, err := os.Lstat(filepath.Join(projectDir, candidate, "bin", "python")); err != nil {
			continue
		}
		guestVenv := workdir + "/" + candidate
		env = appendEnv(env, "VIRTUAL_ENV", guestVenv)
		existingPath := ""
		for _, e := range env {
			if strings.HasPrefix(e, "PATH=") {
				existingPath = strings.TrimPrefix(e, "PATH=")
				break
			}
		}
		if existingPath == "" {
			existingPath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
		}
		env = appendEnv(env, "PATH", guestVenv+"/bin:"+existingPath)
		return env
	}
	return env
}

// mergeCacheMounts appends cache mounts for all `requires` dependencies.
func mergeCacheMounts(tool config.ToolDefinition) []config.CacheMount {
	caches := append([]config.CacheMount(nil), tool.Cache...)
	if len(tool.Requires) == 0 {
		return caches
	}
	global, _ := config.LoadGlobalConfig()
	for _, dep := range tool.Requires {
		var depTool *config.ToolDefinition
		if global != nil {
			if t, ok := global.Tools[dep]; ok {
				t := t
				depTool = &t
			}
		}
		if depTool == nil {
			if t, ok, _ := tools.Lookup(dep, ""); ok {
				depTool = &t
			}
		}
		if depTool != nil {
			caches = append(caches, depTool.Cache...)
		}
	}
	return caches
}

func buildMounts(
	tool config.ToolDefinition, projectDir string, caches []config.CacheMount, pc *config.ProjectConfig,
) ([]bridge.MountSpec, error) {
	var mounts []bridge.MountSpec
	home, _ := os.UserHomeDir()

	if projectDir != "" {
		mounts = append(mounts, bridge.Share(projectDir, tool.Workdir))
	}
	for _, cm := range caches {
		host := cm.Host
		if strings.HasPrefix(host, "~") {
			host = filepath.Join(home, strings.TrimPrefix(host, "~"))
		}
		_ = os.MkdirAll(host, 0o755)
		mounts = append(mounts, bridge.Share(host, cm.Guest))
	}
	if pc != nil && projectDir != "" {
		for _, f := range pc.PassFiles {
			hostFile := filepath.Join(projectDir, f)
			if _, err := os.Stat(hostFile); err != nil {
				continue
			}
			guest := "/workspace/" + f
			mounts = append(mounts, bridge.Share(hostFile, guest).WithOptions("ro"))
		}
	}
	return mounts, nil
}

// sshAgentGuestPath is the in-guest path where silo materializes the relayed
// $SSH_AUTH_SOCK socket. Apple Containerization's UnixSocketConfiguration
// runs the vsock pump under the hood; child processes inherit
// SSH_AUTH_SOCK=<this> and connect transparently.
const sshAgentGuestPath = "/run/silo/ssh-agent.sock"

// sshAgentSocket returns (hostSocketPath, true) when SSH agent forwarding is
// enabled AND the host actually has $SSH_AUTH_SOCK set + reachable.
// Forwarding is silently skipped when the host has no agent — silo can't
// materialize what doesn't exist, and failing the run would break headless /
// CI invocations that opted in via project config.
func sshAgentSocket(tool config.ToolDefinition) (hostSocket string, ok bool) {
	if !tool.PassSshAgent {
		return "", false
	}
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return "", false
	}
	if _, err := os.Stat(sock); err != nil {
		return "", false
	}
	return sock, true
}

// enableISIG flips ISIG on stdin so Ctrl+C still produces SIGINT in raw mode.
func enableISIG() {
	tios, err := unix.IoctlGetTermios(int(os.Stdin.Fd()), unix.TIOCGETA)
	if err != nil {
		return
	}
	tios.Lflag |= unix.ISIG
	_ = unix.IoctlSetTermios(int(os.Stdin.Fd()), unix.TIOCSETA, tios)
}

// resetTerminal restores cooked mode on stdin.
func resetTerminal() {
	tios, err := unix.IoctlGetTermios(int(os.Stdin.Fd()), unix.TIOCGETA)
	if err != nil {
		return
	}
	tios.Lflag |= unix.ICANON | unix.ECHO | unix.ECHOE | unix.ISIG
	tios.Iflag |= unix.ICRNL
	tios.Oflag |= unix.OPOST
	_ = unix.IoctlSetTermios(int(os.Stdin.Fd()), unix.TIOCSETA, tios)
}

// shortID returns 8 hex chars suitable for a container id suffix.
func shortID() string {
	var buf [4]byte
	_, _ = rand.Read(buf[:])
	return hex.EncodeToString(buf[:])
}

// applySetupResourceFloors lifts a tool definition's resource limits to at
// least the minimums that setup scripts typically need. Applied per-call;
// the stored definition is not modified.
func applySetupResourceFloors(t *config.ToolDefinition) {
	const (
		minCPUs         int32  = 4
		minMemoryMB     uint64 = 4096
		minRootfsSizeMB uint64 = 4096
	)
	if t.CPUs < minCPUs {
		t.CPUs = minCPUs
	}
	if t.MemoryMB < minMemoryMB {
		t.MemoryMB = minMemoryMB
	}
	if t.RootfsSizeMB < minRootfsSizeMB {
		t.RootfsSizeMB = minRootfsSizeMB
	}
}

// appendEnv overwrites or appends "KEY=VALUE" in the slice. Env arrays on the
// bridge are just []string of "KEY=VALUE"; we re-read them to avoid building
// a second map when we just need to inject a few proxy vars.
func appendEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i, e := range env {
		if strings.HasPrefix(e, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

// flushGuestSync execs `/bin/sync` inside the running container and waits for
// it to exit. Used post-Wait, pre-Stop to flush the guest page cache to both
// the rootfs ext4 block device and any virtio-fs host shares. Best-effort —
// errors are swallowed because the user's command has already exited and the
// caller will tear the container down regardless.
func flushGuestSync(ctr *bridge.Container) {
	proc, err := ctr.Exec("silo-sync-"+shortID(), bridge.ExecConfig{
		Arguments: []string{"/bin/sync"},
		StdinFD:   -1,
		StdoutFD:  -1,
		StderrFD:  -1,
	})
	if err != nil {
		return
	}
	defer proc.Close()
	if err := proc.Start(); err != nil {
		return
	}
	_, _ = proc.Wait()
}

// copyFile copies src to dst. Uses io.Copy — callers that need APFS clonefile
// should use the cache package directly.
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

// silence unused imports in case syscall is only referenced by build tags on other OSes.
var _ = syscall.Stdin
