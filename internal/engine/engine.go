// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"fmt"
	"io"
	"time"

	"github.com/rchekalov/silo/internal/config"
	"github.com/rchekalov/silo/internal/runtime"
	"github.com/schollz/progressbar/v3"
)

// ContainerEngine is the top-level facade used by CLI commands.
type ContainerEngine struct {
	Global *config.GlobalConfig
}

// NewContainerEngine returns an engine backed by the given global config.
func NewContainerEngine(g *config.GlobalConfig) *ContainerEngine {
	return &ContainerEngine{Global: g}
}

// EnsureRuntime wraps engine.EnsureRuntime() so callers don't need two imports.
func (e *ContainerEngine) EnsureRuntime() error { return EnsureRuntime() }

// RunEphemeralOptions bundles every parameter of RunEphemeral because the
// Rust signature had ten of them and Go cares about clarity more than parity.
type RunEphemeralOptions struct {
	ToolName      string
	Tool          config.ToolDefinition
	Command       string
	Arguments     []string
	ProjectDir    string
	ProjectRoot   string
	ProjectConfig *config.ProjectConfig
	Interactive   bool
	// Stdout, if non-nil, captures the container's stdout. When set, Interactive
	// is forced off and no stdio is forwarded to the host terminal. Used by
	// ExecutableDiscovery to scan PATH.
	Stdout io.Writer
}

// RunEphemeral boots a fresh VM, runs the command, and returns its exit code.
func (e *ContainerEngine) RunEphemeral(opts RunEphemeralOptions) (int32, error) {
	r := newEphemeralRunner(runtime.Kernel(), runtime.Initfs(), runtime.Root())
	return r.Run(opts)
}

// RunSetupOptions configures RunSetup.
type RunSetupOptions struct {
	ToolName      string
	Tool          config.ToolDefinition
	Command       string
	Arguments     []string
	ProjectDir    string
	ProjectRoot   string
	ProjectConfig *config.ProjectConfig
	TargetRootfs  string
	Global        bool
}

// RunSetup runs `command` in a writable VM and persists the rootfs on exit 0.
func (e *ContainerEngine) RunSetup(opts RunSetupOptions) (int32, error) {
	r := newEphemeralRunner(runtime.Kernel(), runtime.Initfs(), runtime.Root())
	return r.RunSetup(opts)
}

// PullImage pulls an OCI reference, optionally caching the rootfs afterwards.
func (e *ContainerEngine) PullImage(reference string, cacheFor *config.ToolDefinition) error {
	bar := progressbar.NewOptions(-1,
		progressbar.OptionSetDescription("Pulling image "+reference),
		progressbar.OptionSpinnerType(14),
		progressbar.OptionThrottle(100*time.Millisecond),
		progressbar.OptionClearOnFinish(),
	)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			default:
				_ = bar.Add(1)
				time.Sleep(100 * time.Millisecond)
			}
		}
	}()

	r := newEphemeralRunner(runtime.Kernel(), runtime.Initfs(), runtime.Root())
	err := r.PullImage(reference, cacheFor)
	close(done)
	_ = bar.Finish()
	if err != nil {
		return err
	}
	if cacheFor != nil {
		fmt.Println("Image pulled and rootfs cached:", reference)
	} else {
		fmt.Println("Image pulled:", reference)
	}
	return nil
}
