// SPDX-License-Identifier: Apache-2.0

// Package shim creates the ~/.silo/bin/ scripts that delegate to `silo run`.
// Each shim is a tiny `sh` script that exec's the silo binary with the right
// tool + --shim arg so that `python foo.py` works as if python were installed.
package shim

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rchekalov/silo/internal/config"
	"github.com/rchekalov/silo/internal/runtime"
)

// Manager owns the shim directory and the silo binary path that shims call.
type Manager struct {
	dir        string
	binaryPath string
}

// NewManager returns a manager for shims under `dir` (empty → ~/.silo/bin/).
// The binary path is detected: prefer /usr/local/bin/silo if installed, else
// fall back to argv[0] (so dev builds also work).
func NewManager(dir string) *Manager {
	if dir == "" {
		dir = runtime.ShimBin()
	}
	bin := "/usr/local/bin/silo"
	if _, err := os.Stat(bin); err != nil {
		if len(os.Args) > 0 {
			bin = os.Args[0]
		} else {
			bin = "silo"
		}
	}
	return &Manager{dir: dir, binaryPath: bin}
}

// SetBinaryPath overrides the resolved binary path (useful for tests).
func (m *Manager) SetBinaryPath(p string) { m.binaryPath = p }

// Dir returns the shim directory path.
func (m *Manager) Dir() string { return m.dir }

// Conflict is "shim X would collide with tool Y".
type Conflict struct {
	Shim      string
	OtherTool string
}

// CheckConflicts returns shims in `tool` that are already claimed by another tool in `cfg`.
func (m *Manager) CheckConflicts(tool config.ToolDefinition, toolName string, cfg *config.GlobalConfig) []Conflict {
	var out []Conflict
	for _, s := range tool.Shims {
		for otherName, other := range cfg.Tools {
			if otherName == toolName {
				continue
			}
			for _, os := range other.Shims {
				if os.HostCommand == s.HostCommand {
					out = append(out, Conflict{Shim: s.HostCommand, OtherTool: otherName})
				}
			}
		}
	}
	return out
}

// CreateShims writes every shim for a tool.
func (m *Manager) CreateShims(tool config.ToolDefinition, toolName string) error {
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return err
	}
	for _, s := range tool.Shims {
		if err := m.writeShim(s, toolName); err != nil {
			return err
		}
	}
	return nil
}

// RemoveShims removes every shim for a tool. Missing entries are ignored.
func (m *Manager) RemoveShims(tool config.ToolDefinition) error {
	for _, s := range tool.Shims {
		p := filepath.Join(m.dir, s.HostCommand)
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// CreateShim writes a single shim.
func (m *Manager) CreateShim(shim config.ShimMapping, toolName string) error {
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return err
	}
	return m.writeShim(shim, toolName)
}

// RemoveShim removes one shim by host command; no-op if absent.
func (m *Manager) RemoveShim(hostCommand string) error {
	p := filepath.Join(m.dir, hostCommand)
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ListShims returns the sorted filenames in the shim directory (excluding dotfiles).
func (m *Manager) ListShims() ([]string, error) {
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out, nil
}

func (m *Manager) writeShim(shim config.ShimMapping, toolName string) error {
	p := filepath.Join(m.dir, shim.HostCommand)
	script := fmt.Sprintf(
		"#!/bin/sh\nexec %q run %s --shim %q -- \"$@\"\n",
		m.binaryPath, toolName, shim.ContainerCommand,
	)
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		return err
	}
	// WriteFile honours the mode on create but not on overwrite; set explicitly.
	return os.Chmod(p, 0o755)
}
