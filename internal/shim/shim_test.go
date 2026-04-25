// SPDX-License-Identifier: Apache-2.0

package shim

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rchekalov/silo/internal/config"
)

func TestShimScriptContent(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	m.SetBinaryPath("/usr/local/bin/silo")

	s := config.ParseShim("pip")
	if err := m.CreateShim(s, "python"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "pip"))
	if err != nil {
		t.Fatal(err)
	}
	// The _SILO_SHIM_DISPATCH=1 prefix marks invocations that came in through
	// a PATH shim, so silo run can decide whether to fall through to the next
	// instance on PATH (pyenv-style) when no project claims the tool.
	if !strings.Contains(string(raw), `exec env _SILO_SHIM_DISPATCH=1 "/usr/local/bin/silo" run python --shim "pip" -- "$@"`) {
		t.Fatalf("unexpected shim content:\n%s", raw)
	}

	info, err := os.Stat(filepath.Join(dir, "pip"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("expected 0755 perms, got %o", info.Mode().Perm())
	}
}

func TestListShims(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	m.SetBinaryPath("/usr/local/bin/silo")
	if err := m.CreateShim(config.ParseShim("python"), "python"); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateShim(config.ParseShim("pip"), "python"); err != nil {
		t.Fatal(err)
	}
	out, err := m.ListShims()
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0] != "pip" || out[1] != "python" {
		t.Fatalf("got %+v", out)
	}
}

func TestRemoveShim(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	m.SetBinaryPath("/usr/local/bin/silo")
	_ = m.CreateShim(config.ParseShim("python"), "python")
	if err := m.RemoveShim("python"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "python")); !os.IsNotExist(err) {
		t.Fatalf("expected removal, got %v", err)
	}
	// second remove is idempotent
	if err := m.RemoveShim("python"); err != nil {
		t.Fatal(err)
	}
}

func TestCheckConflicts(t *testing.T) {
	cfg := &config.GlobalConfig{
		Tools: map[string]config.ToolDefinition{
			"node": {Shims: []config.ShimMapping{config.ParseShim("npm")}},
		},
	}
	newTool := config.ToolDefinition{Shims: []config.ShimMapping{config.ParseShim("npm")}}
	m := NewManager(t.TempDir())
	c := m.CheckConflicts(newTool, "yarn", cfg)
	if len(c) != 1 || c[0].Shim != "npm" || c[0].OtherTool != "node" {
		t.Fatalf("got %+v", c)
	}
}
