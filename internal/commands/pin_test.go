// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rchekalov/silo/internal/config"
)

// withFakeHome redirects ~/.silo/* into a tempdir so setPin's writes don't
// touch the developer's real config. runtime.Root() uses os.UserHomeDir(),
// which honors HOME on Unix.
func withFakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

// seedConfig writes a v2 ~/.silo/config.toml with one tool at the requested
// pin state. Returns the absolute config path.
func seedConfig(t *testing.T, home, tool string, pinned bool) string {
	t.Helper()
	dir := filepath.Join(home, ".silo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.NewGlobalConfig()
	cfg.Tools[tool] = config.ToolDefinition{
		Image:          "registry/" + tool + ":latest",
		PinnedGlobally: pinned,
		Shims: []config.ShimMapping{
			{HostCommand: tool, ContainerCommand: tool},
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	// NewGlobalConfig targets config.toml; LoadGlobalConfig prefers it.
	return filepath.Join(dir, "config.toml")
}

func loadPinState(t *testing.T, tool string) bool {
	t.Helper()
	cfg, err := config.LoadGlobalConfig()
	if err != nil {
		t.Fatal(err)
	}
	def, ok := cfg.Tools[tool]
	if !ok {
		t.Fatalf("tool %q missing after load", tool)
	}
	return def.PinnedGlobally
}

func TestSetPinFlipsAndPersists(t *testing.T) {
	withFakeHome(t)
	seedConfig(t, os.Getenv("HOME"), "node", false)

	// stdout from setPin's success message goes to os.Stdout; redirect it
	// to a pipe so the test output stays clean. We only assert state on
	// disk, not the printed string.
	r, w, _ := os.Pipe()
	stdout := os.Stdout
	os.Stdout = w
	defer func() {
		os.Stdout = stdout
		_ = w.Close()
		_ = r.Close()
	}()

	if err := setPin("node", true); err != nil {
		t.Fatalf("setPin true: %v", err)
	}
	if !loadPinState(t, "node") {
		t.Fatal("expected pinned after setPin(true)")
	}

	if err := setPin("node", false); err != nil {
		t.Fatalf("setPin false: %v", err)
	}
	if loadPinState(t, "node") {
		t.Fatal("expected unpinned after setPin(false)")
	}
}

func TestSetPinIdempotent(t *testing.T) {
	withFakeHome(t)
	seedConfig(t, os.Getenv("HOME"), "node", true)

	// Capture stdout to verify the "already" branch fires (no on-disk
	// change should happen, but the function should not error either).
	r, w, _ := os.Pipe()
	stdout := os.Stdout
	os.Stdout = w
	t.Cleanup(func() {
		os.Stdout = stdout
		_ = r.Close()
	})

	if err := setPin("node", true); err != nil {
		t.Fatalf("idempotent setPin true: %v", err)
	}
	_ = w.Close()
	buf := make([]byte, 256)
	n, _ := r.Read(buf)
	if !strings.Contains(string(buf[:n]), "already") {
		t.Fatalf("expected idempotent message, got %q", string(buf[:n]))
	}
	// Disk state stays correct.
	if !loadPinState(t, "node") {
		t.Fatal("idempotent setPin should not flip state")
	}
}

func TestSetPinErrorsWhenToolNotInstalled(t *testing.T) {
	withFakeHome(t)
	// Create empty config.
	cfg := config.NewGlobalConfig()
	dir := filepath.Join(os.Getenv("HOME"), ".silo")
	_ = os.MkdirAll(dir, 0o755)
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	err := setPin("nonexistent", true)
	if err == nil {
		t.Fatal("expected error for uninstalled tool, got nil")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Fatalf("error should name the tool: %v", err)
	}
}

func TestSetPinPersistsOnDisk(t *testing.T) {
	// Beyond loading via the config package, sanity-check that the on-disk
	// TOML file actually contains the pinnedGlobally field after a flip.
	// Guards against a regression where Save serializes from a stale
	// in-memory copy.
	home := withFakeHome(t)
	path := seedConfig(t, home, "ruby", false)

	r, w, _ := os.Pipe()
	stdout := os.Stdout
	os.Stdout = w
	defer func() {
		os.Stdout = stdout
		_ = w.Close()
		_ = r.Close()
	}()

	if err := setPin("ruby", true); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "pinnedGlobally = true") {
		t.Fatalf("TOML missing pinnedGlobally=true after pin; got:\n%s", raw)
	}
}
