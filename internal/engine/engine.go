// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"fmt"
	"io"

	"github.com/rchekalov/silo/internal/config"
	"github.com/rchekalov/silo/internal/runtime"
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
// The ephemeral runner owns the progress spinner; this wrapper only prints
// the final completion line.
func (e *ContainerEngine) PullImage(reference string, cacheFor *config.ToolDefinition) error {
	r := newEphemeralRunner(runtime.Kernel(), runtime.Initfs(), runtime.Root())
	if err := r.PullImage(reference, cacheFor); err != nil {
		return err
	}
	if cacheFor != nil {
		fmt.Println("Image pulled and rootfs cached:", reference)
	} else {
		fmt.Println("Image pulled:", reference)
	}
	return nil
}
