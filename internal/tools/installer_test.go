// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rchekalov/silo/internal/config"
	"github.com/rchekalov/silo/internal/prompter"
	"github.com/rchekalov/silo/internal/shim"
)

// withFakeSiloHome redirects ~/.silo into a tempdir so installer writes
// (config + shims) don't touch the developer's real silo state.
func withFakeSiloHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

// newTestInstaller builds an Installer with all VM/runtime hooks left nil.
// Resolving an Image-only definition skips registry lookup, so this avoids
// pulling images / booting the bridge entirely.
func newTestInstaller(cfg *config.GlobalConfig) *Installer {
	return &Installer{
		Config:   cfg,
		Shims:    shim.NewManager(""), // ~/.silo/bin under fake HOME
		Prompter: prompter.NewScripted(),
		// All other hooks nil: EnsureRuntime, PullImage, RunCaptured, RunSetup.
	}
}

func TestInstallerSetsPinnedGlobally(t *testing.T) {
	withFakeSiloHome(t)
	cfg := config.NewGlobalConfig()
	in := newTestInstaller(cfg)

	def, err := in.Install(InstallOptions{
		Name:  "tool",
		Image: "registry.example/tool:1.0",
		Shims: []string{"tool"},
	})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	// Both the returned def and the on-config copy must reflect the pin.
	if !def.PinnedGlobally {
		t.Fatal("returned def should have PinnedGlobally=true after silo install")
	}
	if !cfg.Tools["tool"].PinnedGlobally {
		t.Fatal("config Tools entry should have PinnedGlobally=true")
	}

	// The flag should round-trip to disk so subsequent loads still see it.
	loaded, err := config.LoadGlobalConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Tools["tool"].PinnedGlobally {
		t.Fatal("PinnedGlobally must persist across save/load")
	}

	// Sanity-check the YAML literally has the field — guards against an
	// omitempty mishap that would silently drop the flag on save.
	raw, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".silo", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "pinnedGlobally: true") {
		t.Fatalf("config.yaml missing pinnedGlobally:true; got:\n%s", raw)
	}
}

func TestSyncPathLeavesPinnedGloballyFalse(t *testing.T) {
	// commands/pull.go's executePlan calls global.InstallTool(p.name, p.def)
	// where p.def comes from the registry without the flag set. Mimic that
	// boundary directly: a ToolDefinition with PinnedGlobally=false, written
	// via InstallTool, must surface as unpinned on subsequent load.
	withFakeSiloHome(t)
	cfg := config.NewGlobalConfig()
	def := config.ToolDefinition{
		Image: "registry.example/from-sync:1.0",
		Shims: []config.ShimMapping{{HostCommand: "from-sync", ContainerCommand: "from-sync"}},
		// PinnedGlobally intentionally zero-value (false) — sync must not flip it.
	}
	if err := cfg.InstallTool("from-sync", def); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.LoadGlobalConfig()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Tools["from-sync"].PinnedGlobally {
		t.Fatal("sync-style InstallTool with PinnedGlobally=false must not become pinned on load")
	}
	// Field must be omitted from YAML when false (omitempty), so the file
	// stays clean for sync-installed tools.
	raw, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".silo", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "pinnedGlobally") {
		t.Fatalf("YAML should omit pinnedGlobally when false; got:\n%s", raw)
	}
}
