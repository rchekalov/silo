// SPDX-License-Identifier: Apache-2.0

// Package tools owns the built-in tool registry and lookup logic.
package tools

import (
	_ "embed"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/rchekalov/silo/internal/config"
	"github.com/rchekalov/silo/internal/runtime"
	"gopkg.in/yaml.v3"
)

//go:embed registry.yaml
var builtinRegistryYAML []byte

// VersionEntry is one selectable tag in a tool's `versions:` list.
type VersionEntry struct {
	Tag     string `yaml:"tag"`
	Label   string `yaml:"label,omitempty"`
	Default bool   `yaml:"default,omitempty"`
}

// RegistryEntry is the on-disk shape of one tool in registry.yaml.
// It's a superset of ToolDefinition (adds description + versions) with
// "all fields optional" semantics so defaults can fill in missing values.
type RegistryEntry struct {
	Description  string                `yaml:"description,omitempty"`
	Image        string                `yaml:"image"`
	Versions     []VersionEntry        `yaml:"versions,omitempty"`
	Shims        []config.ShimMapping  `yaml:"shims,omitempty"`
	Cache        []config.CacheMount   `yaml:"cache,omitempty"`
	Workdir      string                `yaml:"workdir,omitempty"`
	Env          map[string]string     `yaml:"env,omitempty"`
	CPUs         *int32                `yaml:"cpus,omitempty"`
	MemoryMB     *uint64               `yaml:"memoryMB,omitempty"`
	RootfsSizeMB *uint64               `yaml:"rootfsSizeMB,omitempty"`
	Network      *config.NetworkConfig `yaml:"network,omitempty"`
	Requires     []string              `yaml:"requires,omitempty"`
	Ports        []config.PortMapping  `yaml:"ports,omitempty"`
	LSP          *config.LspConfig     `yaml:"lsp,omitempty"`
	PostInstall  []string              `yaml:"postInstall,omitempty"`
}

// ToToolDefinition returns a filled-in ToolDefinition, optionally overriding
// the image tag with `version` (replaces the part after the last `:`).
func (e RegistryEntry) ToToolDefinition(version string) config.ToolDefinition {
	image := e.Image
	if version != "" {
		if i := strings.LastIndex(image, ":"); i >= 0 {
			image = image[:i]
		}
		image += ":" + version
	}
	def := config.ToolDefinition{
		Image:       image,
		Shims:       append([]config.ShimMapping(nil), e.Shims...),
		Cache:       append([]config.CacheMount(nil), e.Cache...),
		Workdir:     e.Workdir,
		Env:         cloneStringMap(e.Env),
		Network:     cloneNetwork(e.Network),
		Requires:    append([]string(nil), e.Requires...),
		Ports:       append([]config.PortMapping(nil), e.Ports...),
		LSP:         cloneLsp(e.LSP),
		PostInstall: append([]string(nil), e.PostInstall...),
	}
	if e.CPUs != nil {
		def.CPUs = *e.CPUs
	}
	if e.MemoryMB != nil {
		def.MemoryMB = *e.MemoryMB
	}
	if e.RootfsSizeMB != nil {
		def.RootfsSizeMB = *e.RootfsSizeMB
	}
	def.ApplyDefaults()
	return def
}

type registryFile struct {
	Tools map[string]RegistryEntry `yaml:"tools"`
}

// Entries returns the union of the built-in registry and any user overrides
// in ~/.silo/registry.yaml. User entries replace built-ins by the same key.
func Entries() (map[string]RegistryEntry, error) {
	builtin, err := loadRegistry(builtinRegistryYAML)
	if err != nil {
		return nil, fmt.Errorf("built-in registry: %w", err)
	}
	user, err := loadUserRegistry()
	if err != nil {
		return nil, err
	}
	for k, v := range user {
		builtin[k] = v
	}
	return builtin, nil
}

// ParseSpec splits a "tool@version" string. A bare "tool" returns ("tool", "").
// Empty inputs, a leading "@", or an empty tag after the separator are reported
// as errors so callers don't silently drop user intent.
func ParseSpec(spec string) (name, version string, err error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", "", fmt.Errorf("empty tool spec")
	}
	name, version, hasAt := strings.Cut(spec, "@")
	if name == "" {
		return "", "", fmt.Errorf("invalid tool spec %q: missing name", spec)
	}
	if hasAt && version == "" {
		return "", "", fmt.Errorf("invalid tool spec %q: empty version after %q", spec, "@")
	}
	return name, version, nil
}

// Lookup returns the tool definition for `name`, with optional `version`.
// When `version` is set and the registry entry declares a non-empty Versions
// list, the version must match one of the declared tags exactly. Previously
// any string was accepted and silently rewrote the image reference — users
// asking for `python@3.13` would land on `python:3.13` (the full ~900 MB
// debian variant) instead of the intended slim base.
func Lookup(name, version string) (config.ToolDefinition, bool, error) {
	entries, err := Entries()
	if err != nil {
		return config.ToolDefinition{}, false, err
	}
	e, ok := entries[name]
	if !ok {
		return config.ToolDefinition{}, false, nil
	}
	if version != "" && len(e.Versions) > 0 {
		known := false
		tags := make([]string, 0, len(e.Versions))
		for _, v := range e.Versions {
			tags = append(tags, v.Tag)
			if v.Tag == version {
				known = true
				break
			}
		}
		if !known {
			return config.ToolDefinition{}, false, fmt.Errorf(
				"unknown version %q for %q — available: %s (or pass --image docker.io/library/%s:%s to force a custom tag)",
				version, name, strings.Join(tags, ", "), name, version,
			)
		}
	}
	return e.ToToolDefinition(version), true, nil
}

// LookupEntry returns the raw RegistryEntry (metadata included).
func LookupEntry(name string) (RegistryEntry, bool, error) {
	entries, err := Entries()
	if err != nil {
		return RegistryEntry{}, false, err
	}
	e, ok := entries[name]
	return e, ok, nil
}

// AvailableTools returns a sorted list of registered tool names.
func AvailableTools() ([]string, error) {
	entries, err := Entries()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for k := range entries {
		names = append(names, k)
	}
	sort.Strings(names)
	return names, nil
}

// DefaultVersion returns the `default: true` tag for `name`, or "" if the tool
// has no version list. Errors propagate.
func DefaultVersion(name string) (string, error) {
	entries, err := Entries()
	if err != nil {
		return "", err
	}
	e, ok := entries[name]
	if !ok || len(e.Versions) == 0 {
		return "", nil
	}
	for _, v := range e.Versions {
		if v.Default {
			return v.Tag, nil
		}
	}
	return e.Versions[0].Tag, nil
}

func loadRegistry(raw []byte) (map[string]RegistryEntry, error) {
	var f registryFile
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return nil, err
	}
	if f.Tools == nil {
		f.Tools = map[string]RegistryEntry{}
	}
	return f.Tools, nil
}

func loadUserRegistry() (map[string]RegistryEntry, error) {
	raw, err := os.ReadFile(runtime.UserRegistry())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read user registry: %w", err)
	}
	return loadRegistry(raw)
}

func cloneStringMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func cloneNetwork(n *config.NetworkConfig) *config.NetworkConfig {
	if n == nil {
		return nil
	}
	out := *n
	if n.Proxy != nil {
		p := *n.Proxy
		p.Allow = append([]string(nil), n.Proxy.Allow...)
		p.Deny = append([]string(nil), n.Proxy.Deny...)
		out.Proxy = &p
	}
	return &out
}

func cloneLsp(l *config.LspConfig) *config.LspConfig {
	if l == nil {
		return nil
	}
	out := *l
	out.Command = append([]string(nil), l.Command...)
	out.Cache = append([]config.CacheMount(nil), l.Cache...)
	out.Env = cloneStringMap(l.Env)
	return &out
}
