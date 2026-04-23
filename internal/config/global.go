// SPDX-License-Identifier: Apache-2.0

// Package config contains on-disk configuration types:
//
//   - GlobalConfig  (~/.silo/config.yaml) — installed tools
//   - ProjectConfig (.siloconf in cwd or walked-up) + ~/.silo/siloconf fallback
//   - ToolDefinition — tool metadata (image, shims, cache, env, limits)
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/rchekalov/silo/internal/runtime"
)

// GlobalConfig is ~/.silo/config.yaml — the list of installed tools.
type GlobalConfig struct {
	Version int                       `yaml:"version"`
	Tools   map[string]ToolDefinition `yaml:"tools,omitempty"`

	// path is where this config was loaded from / will be saved to.
	// Defaults to runtime.Config(); overridden in tests.
	path string `yaml:"-"`
}

// NewGlobalConfig returns an empty v1 config targeting the default path.
func NewGlobalConfig() *GlobalConfig {
	return &GlobalConfig{Version: 1, Tools: map[string]ToolDefinition{}, path: runtime.Config()}
}

// LoadGlobalConfig reads ~/.silo/config.yaml. If missing, returns an empty config.
func LoadGlobalConfig() (*GlobalConfig, error) {
	return LoadGlobalConfigAt(runtime.Config())
}

// LoadGlobalConfigAt reads the file at `path`. If missing, returns an empty config
// with that path set for subsequent Save calls. Useful for tests.
func LoadGlobalConfigAt(path string) (*GlobalConfig, error) {
	c := &GlobalConfig{Version: 1, Tools: map[string]ToolDefinition{}, path: path}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if err := yaml.Unmarshal(raw, c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if c.Tools == nil {
		c.Tools = map[string]ToolDefinition{}
	}
	if c.Version == 0 {
		c.Version = 1
	}
	c.path = path
	return c, nil
}

// Save writes the config back to its origin path, creating parent dirs as needed.
func (c *GlobalConfig) Save() error {
	if c.path == "" {
		c.path = runtime.Config()
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	out, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(c.path, out, 0o644)
}

// InstallTool adds or replaces `name` then saves.
func (c *GlobalConfig) InstallTool(name string, def ToolDefinition) error {
	if c.Tools == nil {
		c.Tools = map[string]ToolDefinition{}
	}
	c.Tools[name] = def
	return c.Save()
}

// UninstallTool removes `name` then saves (no-op if absent).
func (c *GlobalConfig) UninstallTool(name string) error {
	delete(c.Tools, name)
	return c.Save()
}

// ResolveShim returns the tool name and its definition that owns `shim`, or "",nil.
func (c *GlobalConfig) ResolveShim(shim string) (string, *ToolDefinition) {
	for name, tool := range c.Tools {
		for _, s := range tool.Shims {
			if s.HostCommand == shim {
				t := tool
				return name, &t
			}
		}
	}
	return "", nil
}

// ResolveShimAll returns every tool name whose shims include `shim`, sorted.
// Used by callers that need to detect ambiguity (multiple owners).
func (c *GlobalConfig) ResolveShimAll(shim string) []string {
	var names []string
	for name, tool := range c.Tools {
		for _, s := range tool.Shims {
			if s.HostCommand == shim {
				names = append(names, name)
				break
			}
		}
	}
	sort.Strings(names)
	return names
}
