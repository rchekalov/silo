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

	toml "github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"

	"github.com/rchekalov/silo/internal/runtime"
)

// GlobalConfigVersion is the current schema version of ~/.silo/config.yaml.
//
//   - v1: original layout. Every installed tool is implicitly globally claimed
//     (silo always handles its shims).
//   - v2: adds ToolDefinition.PinnedGlobally. Old v1 entries migrate by
//     defaulting PinnedGlobally=true on load (preserves prior behavior). Fresh
//     entries written by `silo sync` set it to false so shim invocations fall
//     through to the next instance on PATH outside silo projects.
const GlobalConfigVersion = 2

// GlobalConfig is ~/.silo/config.yaml — the list of installed tools.
type GlobalConfig struct {
	Version int                       `yaml:"version"        toml:"version"`
	Tools   map[string]ToolDefinition `yaml:"tools,omitempty" toml:"tools,omitempty"`

	// path is where this config was loaded from / will be saved to.
	// Defaults to runtime.Config(); overridden in tests.
	path string `yaml:"-" toml:"-"`
}

// NewGlobalConfig returns an empty config at the current schema version.
// Newly-created configs target the TOML path so writes go to config.toml.
func NewGlobalConfig() *GlobalConfig {
	return &GlobalConfig{Version: GlobalConfigVersion, Tools: map[string]ToolDefinition{}, path: runtime.ConfigTOML()}
}

// LoadGlobalConfig reads the global config. Prefers ~/.silo/config.toml; falls
// back to the legacy ~/.silo/config.yaml. Returns an empty config if neither
// is present, targeting config.toml for subsequent saves.
func LoadGlobalConfig() (*GlobalConfig, error) {
	if _, err := os.Stat(runtime.ConfigTOML()); err == nil {
		return LoadGlobalConfigAt(runtime.ConfigTOML())
	}
	if _, err := os.Stat(runtime.Config()); err == nil {
		return LoadGlobalConfigAt(runtime.Config())
	}
	return LoadGlobalConfigAt(runtime.ConfigTOML())
}

// LoadGlobalConfigAt reads the file at `path`. If missing, returns an empty
// config targeting that path for subsequent Save calls. Format is sniffed by
// extension: .toml uses TOML; anything else uses YAML for backward compat.
// Useful for tests.
func LoadGlobalConfigAt(path string) (*GlobalConfig, error) {
	c := &GlobalConfig{Version: 1, Tools: map[string]ToolDefinition{}, path: path}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if filepath.Ext(path) == ".toml" {
		if err := toml.Unmarshal(raw, c); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
	} else {
		if err := yaml.Unmarshal(raw, c); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
	}
	if c.Tools == nil {
		c.Tools = map[string]ToolDefinition{}
	}
	if c.Version == 0 {
		c.Version = 1
	}
	// v1 → v2 migration: every existing tool was implicitly globally claimed,
	// so default PinnedGlobally=true for them. Future installs set the flag
	// explicitly: `silo install` → true, `silo sync` → false. Bump the on-disk
	// version so subsequent loads read the field as authoritative.
	if c.Version < GlobalConfigVersion {
		for name, t := range c.Tools {
			t.PinnedGlobally = true
			c.Tools[name] = t
		}
		c.Version = GlobalConfigVersion
	}
	c.path = path
	return c, nil
}

// Save writes the config back to its origin path, creating parent dirs as
// needed. Format is decided by the path extension — .toml emits TOML; anything
// else emits YAML. New installs use config.toml (set by NewGlobalConfig); the
// legacy config.yaml stays in place until the user runs `silo config migrate`.
func (c *GlobalConfig) Save() error {
	if c.path == "" {
		c.path = runtime.ConfigTOML()
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	var out []byte
	var err error
	if filepath.Ext(c.path) == ".toml" {
		out, err = toml.Marshal(c)
	} else {
		out, err = yaml.Marshal(c)
	}
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
