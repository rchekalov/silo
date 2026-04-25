// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Defaults match the Rust port exactly.
const (
	DefaultWorkdir      = "/workspace"
	DefaultCPUs         = 2
	DefaultMemoryMB     = 2048
	DefaultRootfsSizeMB = 2048
)

// ProxyConfig is a domain allow/deny list for the network proxy. `allow`
// supports leading wildcards ("*.github.com").
type ProxyConfig struct {
	Allow []string `yaml:"allow"`
	Deny  []string `yaml:"deny,omitempty"`
}

// NetworkConfig gates host access for a tool. Camel-case in YAML.
type NetworkConfig struct {
	HostAccess bool         `yaml:"hostAccess"`
	Proxy      *ProxyConfig `yaml:"proxy,omitempty"`
}

// PortMapping forwards a host TCP port to a guest VM port.
type PortMapping struct {
	Host  uint16 `yaml:"host"`
	Guest uint16 `yaml:"guest"`
}

// ShimMapping pairs a host shim filename with the command run inside the container.
// Serialized as a plain string: "python" (1:1) or "npm2:npm" (remap).
type ShimMapping struct {
	HostCommand      string
	ContainerCommand string
}

// ParseShim builds a ShimMapping from a "host[:container]" spec.
func ParseShim(spec string) ShimMapping {
	if host, container, ok := strings.Cut(spec, ":"); ok {
		return ShimMapping{HostCommand: host, ContainerCommand: container}
	}
	return ShimMapping{HostCommand: spec, ContainerCommand: spec}
}

// String implements fmt.Stringer.
func (s ShimMapping) String() string {
	if s.HostCommand == s.ContainerCommand {
		return s.HostCommand
	}
	return s.HostCommand + ":" + s.ContainerCommand
}

// MarshalYAML emits the shim as a scalar string.
func (s ShimMapping) MarshalYAML() (any, error) { return s.String(), nil }

// UnmarshalYAML parses a scalar string into a ShimMapping.
func (s *ShimMapping) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("shim mapping must be a string, got kind %d", node.Kind)
	}
	*s = ParseShim(node.Value)
	return nil
}

// CacheMount is a persistent host<->guest path binding. Sized hints are purely
// informational (shown in `silo list`).
//
// NoGC opts the mount out of `silo cache gc --tool-caches`. Use it for mounts
// that hold durable state (OAuth credentials, user config) rather than
// regenerable cache — the age-based pass would silently delete those files
// after MaxAge, forcing the user to re-authenticate with no obvious cause.
type CacheMount struct {
	Guest    string `yaml:"guest"`
	Host     string `yaml:"host"`
	SizeHint string `yaml:"sizeHint,omitempty"`
	NoGC     bool   `yaml:"noGC,omitempty"`
}

// LspConfig describes an optional language-server installation/run recipe.
type LspConfig struct {
	Command []string          `yaml:"command"`
	Install string            `yaml:"install,omitempty"`
	Cache   []CacheMount      `yaml:"cache,omitempty"`
	Env     map[string]string `yaml:"env,omitempty"`
}

// ToolDefinition captures everything we need to create a VM and run a tool.
type ToolDefinition struct {
	Image   string            `yaml:"image"`
	Shims   []ShimMapping     `yaml:"shims,omitempty"`
	Cache   []CacheMount      `yaml:"cache,omitempty"`
	Workdir string            `yaml:"workdir,omitempty"`
	Env     map[string]string `yaml:"env,omitempty"`
	// PassEnv lists host env var names copied into the guest when set. Used by
	// registry entries to declare the credential env their tool expects (e.g.
	// claude-code → ANTHROPIC_API_KEY) so the tool works out of the box without
	// the user wiring passEnv in every project's .siloconf. Merged with the
	// project-level passEnv at runtime.
	PassEnv      []string       `yaml:"passEnv,omitempty"`
	CPUs         int32          `yaml:"cpus,omitempty"`
	MemoryMB     uint64         `yaml:"memoryMB,omitempty"`
	RootfsSizeMB uint64         `yaml:"rootfsSizeMB,omitempty"`
	Network      *NetworkConfig `yaml:"network,omitempty"`
	Requires     []string       `yaml:"requires,omitempty"`
	Ports        []PortMapping  `yaml:"ports,omitempty"`
	BuildRootfs  string         `yaml:"buildRootfs,omitempty"`
	BuildScript  string         `yaml:"buildScript,omitempty"`
	// PostInstall is a list of shell commands baked into a persistent rootfs
	// right after the image is pulled. The final rootfs becomes BuildRootfs
	// (global scope) so subsequent `silo run` invocations reuse it without
	// refetching anything from the registry. The build step uses HostAccess
	// networking without the proxy allowlist, so apt-get / npm install / etc.
	// work regardless of the runtime's tighter allowlist.
	PostInstall []string `yaml:"postInstall,omitempty"`
	// BuildScope records how BuildRootfs/BuildScript were produced so that
	// `silo rebuild` picks the right target without guessing from the
	// filesystem. Values: "global" (shared ~/.silo/builds/<tool>), "project"
	// (pinned to BuildProjectRoot), or "" (legacy entries predating this field).
	BuildScope       string     `yaml:"buildScope,omitempty"`
	BuildProjectRoot string     `yaml:"buildProjectRoot,omitempty"`
	LSP              *LspConfig `yaml:"lsp,omitempty"`
}

// ApplyDefaults fills any zero-valued fields with the defaults.
func (t *ToolDefinition) ApplyDefaults() {
	if t.Workdir == "" {
		t.Workdir = DefaultWorkdir
	}
	if t.CPUs == 0 {
		t.CPUs = DefaultCPUs
	}
	if t.MemoryMB == 0 {
		t.MemoryMB = DefaultMemoryMB
	}
	if t.RootfsSizeMB == 0 {
		t.RootfsSizeMB = DefaultRootfsSizeMB
	}
	if t.Env == nil {
		t.Env = map[string]string{}
	}
}

// NewToolDefinition returns a ToolDefinition with defaults applied.
func NewToolDefinition() ToolDefinition {
	t := ToolDefinition{}
	t.ApplyDefaults()
	return t
}

// ApplyOverride returns a copy of def with any non-zero fields from o applied.
// Mirrors the semantics used at run-time (see engine.resolveOverrides) so
// `silo pull` prepares the exact image/network/ports that `silo run` will use.
// Env maps are merged (override wins per key); Ports replace wholesale if set.
func ApplyOverride(def ToolDefinition, o ToolOverride) ToolDefinition {
	out := def
	if o.Image != "" {
		out.Image = o.Image
	}
	if len(o.Env) > 0 {
		merged := make(map[string]string, len(def.Env)+len(o.Env))
		for k, v := range def.Env {
			merged[k] = v
		}
		for k, v := range o.Env {
			merged[k] = v
		}
		out.Env = merged
	}
	if o.Network != nil {
		n := *o.Network
		if o.Network.Proxy != nil {
			p := *o.Network.Proxy
			p.Allow = append([]string(nil), o.Network.Proxy.Allow...)
			p.Deny = append([]string(nil), o.Network.Proxy.Deny...)
			n.Proxy = &p
		}
		out.Network = &n
	}
	if o.Ports != nil {
		out.Ports = append([]PortMapping(nil), o.Ports...)
	}
	if len(o.PostInstall) > 0 {
		// Registry steps run first (base image prep), then project steps on top.
		// Detaching with append([]string(nil), ...) so neither input slice is shared.
		combined := append([]string(nil), def.PostInstall...)
		combined = append(combined, o.PostInstall...)
		out.PostInstall = combined
	}
	if len(o.Cache) > 0 {
		out.Cache = mergeCacheMounts(def.Cache, o.Cache)
	}
	if o.CPUs != 0 {
		out.CPUs = o.CPUs
	}
	if o.MemoryMB != 0 {
		out.MemoryMB = o.MemoryMB
	}
	if o.RootfsSizeMB != 0 {
		out.RootfsSizeMB = o.RootfsSizeMB
	}
	if o.Workdir != "" {
		out.Workdir = o.Workdir
	}
	if len(o.PassEnv) > 0 {
		// Append override entries to the base, deduping while preserving order.
		seen := make(map[string]struct{}, len(def.PassEnv)+len(o.PassEnv))
		merged := make([]string, 0, len(def.PassEnv)+len(o.PassEnv))
		for _, k := range def.PassEnv {
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
		out.PassEnv = merged
	}
	if o.LSP != nil {
		out.LSP = mergeLspConfig(def.LSP, o.LSP)
	}
	return out
}

// ExtraPostInstall returns the override's postInstall steps (what was appended
// on top of the registry base). It is a convenience for callers that need to
// know whether a project added any bake steps without recomputing the diff.
//
// The slice is a fresh copy — safe to mutate.
func (o ToolOverride) ExtraPostInstall() []string {
	if len(o.PostInstall) == 0 {
		return nil
	}
	return append([]string(nil), o.PostInstall...)
}
