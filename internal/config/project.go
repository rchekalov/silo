// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/rchekalov/silo/internal/runtime"
)

// ProjectConfigFilename is the per-project config file name that's walked up
// from the current working directory.
const ProjectConfigFilename = ".siloconf"

// MountConfig configures the /workspace mount.
type MountConfig struct {
	Mode    string   `yaml:"mode,omitempty"`
	Exclude []string `yaml:"exclude,omitempty"`
}

// ToolOverride captures per-project tweaks to a tool definition.
//
// PostInstall extends the registry's postInstall with project-specific bake
// steps (e.g. installing JDK + Kotlin into claude-code for a JVM project).
// Steps from the override are appended to the registry's list, so the base
// image layout stays intact. Presence of extra steps triggers `silo sync`
// to produce a project-scoped rootfs at <projectRoot>/.silo/<tool>/rootfs.ext4.
//
// Cache lets a project add persistent host<->guest mounts on top of the
// registry's. Deduplication is by Guest path (override wins on conflict).
type ToolOverride struct {
	Image       string            `yaml:"image,omitempty"`
	Env         map[string]string `yaml:"env,omitempty"`
	Network     *NetworkConfig    `yaml:"network,omitempty"`
	Ports       []PortMapping     `yaml:"ports,omitempty"`
	PostInstall []string          `yaml:"postInstall,omitempty"`
	Cache       []CacheMount      `yaml:"cache,omitempty"`
	// CPUs / MemoryMB / RootfsSizeMB override the registry/global resource
	// defaults on a per-project basis. Zero means "no override" — the base
	// ToolDefinition's value wins. Tag spelling matches ToolDefinition so
	// `silo config show` round-trips and global vs project keys stay aligned.
	CPUs         int32  `yaml:"cpus,omitempty"`
	MemoryMB     uint64 `yaml:"memoryMB,omitempty"`
	RootfsSizeMB uint64 `yaml:"rootfsSizeMB,omitempty"`
	// Workdir overrides the guest working directory (e.g. monorepos that mount
	// the project at /app instead of /workspace). Empty string means "no override".
	Workdir string `yaml:"workdir,omitempty"`
	// PassEnv adds host env var names that should be copied into the guest for
	// this tool only. Use it for credentials scoped to one tool (e.g. only
	// `claude-code` should see ANTHROPIC_API_KEY). Merged with the base
	// ToolDefinition.PassEnv and the project-level PassEnv.
	PassEnv []string `yaml:"passEnv,omitempty"`
	// LSP overrides bits of the registry's LspConfig: pin a language-server
	// install command, add LSP-only cache mounts, tweak LSP env. Non-empty
	// fields win over the base; nil sub-fields leave the base intact.
	LSP *LspConfig `yaml:"lsp,omitempty"`
}

// ProjectConfig is .siloconf at the project root (or ~/.silo/siloconf, globally).
type ProjectConfig struct {
	// Tools lists the tools this project depends on. Keys in Overrides also count;
	// see ProjectTools. Declaring a tool here lets the user pin it without a
	// customization block, which is the common case.
	Tools     []string                `yaml:"tools,omitempty"`
	PassEnv   []string                `yaml:"passEnv,omitempty"`
	PassFiles []string                `yaml:"passFiles,omitempty"`
	Mount     *MountConfig            `yaml:"mount,omitempty"`
	Overrides map[string]ToolOverride `yaml:"overrides,omitempty"`
	Cache     *CacheConfig            `yaml:"cache,omitempty"`
	// ProjectID is an optional stable identifier (e.g. UUID/ULID) for this
	// project. Without it, silo keys per-machine state under a hash of the
	// project's current absolute path, which means renaming or moving the
	// project directory orphans that state (smart-adoption recovers most
	// cases by matching .siloconf content). Set this once and silo's state
	// survives `mv` unconditionally. Mirrors compose.yaml's `name:` field.
	ProjectID string `yaml:"project_id,omitempty"`
}

// ProjectTools returns the sorted, deduplicated set of tools required by this
// project: the union of `tools:` and the keys of `overrides:`. Used by
// `silo pull` and `silo clean` to find the project's tool set.
func (c *ProjectConfig) ProjectTools() []string {
	if c == nil {
		return nil
	}
	seen := map[string]struct{}{}
	for _, t := range c.Tools {
		seen[t] = struct{}{}
	}
	for name := range c.Overrides {
		seen[name] = struct{}{}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// LoadProjectConfigFile parses a .siloconf file at path. Returns (nil, nil) if absent.
func LoadProjectConfigFile(path string) (*ProjectConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var c ProjectConfig
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &c, nil
}

// LoadGlobalSiloconf reads ~/.silo/siloconf, if it exists.
func LoadGlobalSiloconf() (*ProjectConfig, error) {
	return LoadProjectConfigFile(runtime.GlobalSiloconf())
}

// FindProjectConfig walks up from `start` (default cwd) looking for .siloconf.
// Returns (config, root) or (nil, ""). Errors propagate.
func FindProjectConfig(start string) (*ProjectConfig, string, error) {
	if start == "" {
		var err error
		start, err = os.Getwd()
		if err != nil {
			return nil, "", err
		}
	}
	current, err := filepath.Abs(start)
	if err != nil {
		return nil, "", err
	}
	for {
		candidate := filepath.Join(current, ProjectConfigFilename)
		cfg, err := LoadProjectConfigFile(candidate)
		if err != nil {
			return nil, "", err
		}
		if cfg != nil {
			return cfg, current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil, "", nil
		}
		current = parent
	}
}

// FindMergedProjectConfig walks up for .siloconf and merges it over
// ~/.silo/siloconf (project wins). Returns (merged, root or "", err).
// If neither exists, returns (nil, "", nil).
func FindMergedProjectConfig(start string) (*ProjectConfig, string, error) {
	global, err := LoadGlobalSiloconf()
	if err != nil {
		return nil, "", err
	}
	project, root, err := FindProjectConfig(start)
	if err != nil {
		return nil, "", err
	}
	switch {
	case project != nil && global != nil:
		merged := project.MergeOver(global)
		return &merged, root, nil
	case project != nil:
		return project, root, nil
	case global != nil:
		return global, "", nil
	default:
		return nil, "", nil
	}
}

// FindOrDefault returns an existing project config walked up from cwd, or an
// empty one rooted at cwd.
func FindOrDefault() (*ProjectConfig, string, error) {
	cfg, root, err := FindProjectConfig("")
	if err != nil {
		return nil, "", err
	}
	if cfg != nil {
		return cfg, root, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, "", err
	}
	return &ProjectConfig{}, cwd, nil
}

// Save writes the YAML to <directory>/.siloconf.
func (c *ProjectConfig) Save(directory string) error {
	out, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(directory, ProjectConfigFilename), out, 0o644)
}

// MergeOver returns a new config with `c` merged over `base`. `c` wins on
// conflicts. PassEnv / PassFiles are deduplicated preserving order.
func (c *ProjectConfig) MergeOver(base *ProjectConfig) ProjectConfig {
	out := ProjectConfig{
		Tools:     dedupMerge(base.Tools, c.Tools),
		PassEnv:   dedupMerge(base.PassEnv, c.PassEnv),
		PassFiles: dedupMerge(base.PassFiles, c.PassFiles),
		ProjectID: c.ProjectID,
	}
	if out.ProjectID == "" {
		out.ProjectID = base.ProjectID
	}
	if c.Mount != nil {
		mc := *c.Mount
		out.Mount = &mc
	} else if base.Mount != nil {
		mc := *base.Mount
		out.Mount = &mc
	}
	if c.Cache != nil {
		cc := *c.Cache
		out.Cache = &cc
	} else if base.Cache != nil {
		cc := *base.Cache
		out.Cache = &cc
	}
	// Clone base overrides, then merge c overrides on top.
	merged := map[string]ToolOverride{}
	for k, v := range base.Overrides {
		merged[k] = v
	}
	for tool, override := range c.Overrides {
		existing, ok := merged[tool]
		if !ok {
			merged[tool] = override
			continue
		}
		if override.Image != "" {
			existing.Image = override.Image
		}
		if len(override.Env) > 0 {
			if existing.Env == nil {
				existing.Env = map[string]string{}
			}
			for k, v := range override.Env {
				existing.Env[k] = v
			}
		}
		if override.Network != nil {
			n := *override.Network
			existing.Network = &n
		}
		if override.Ports != nil {
			existing.Ports = append([]PortMapping(nil), override.Ports...)
		}
		if len(override.PostInstall) > 0 {
			// Both sides are project-level overrides — base may come from the
			// global siloconf. Append so shared global setup steps run first.
			existing.PostInstall = append(append([]string(nil), existing.PostInstall...), override.PostInstall...)
		}
		if len(override.Cache) > 0 {
			existing.Cache = mergeCacheMounts(existing.Cache, override.Cache)
		}
		if override.CPUs != 0 {
			existing.CPUs = override.CPUs
		}
		if override.MemoryMB != 0 {
			existing.MemoryMB = override.MemoryMB
		}
		if override.RootfsSizeMB != 0 {
			existing.RootfsSizeMB = override.RootfsSizeMB
		}
		if override.Workdir != "" {
			existing.Workdir = override.Workdir
		}
		if len(override.PassEnv) > 0 {
			existing.PassEnv = dedupMerge(existing.PassEnv, override.PassEnv)
		}
		if override.LSP != nil {
			existing.LSP = mergeLspConfig(existing.LSP, override.LSP)
		}
		merged[tool] = existing
	}
	if len(merged) > 0 {
		out.Overrides = merged
	}
	return out
}

// ensureToolOverride returns the overrides entry for `tool`, creating it if missing.
func (c *ProjectConfig) ensureToolOverride(tool string) *ToolOverride {
	if c.Overrides == nil {
		c.Overrides = map[string]ToolOverride{}
	}
	if _, ok := c.Overrides[tool]; !ok {
		c.Overrides[tool] = ToolOverride{}
	}
	// To mutate in place we store back at the end. Return a pointer into a temp
	// by re-reading from the map after edits; simpler: operate via getter+setter.
	o := c.Overrides[tool]
	return &o
}

func (c *ProjectConfig) setToolOverride(tool string, o ToolOverride) {
	if c.Overrides == nil {
		c.Overrides = map[string]ToolOverride{}
	}
	c.Overrides[tool] = o
}

// AddTool appends `name` to Tools if not already present. Idempotent.
func (c *ProjectConfig) AddTool(name string) {
	for _, t := range c.Tools {
		if t == name {
			return
		}
	}
	c.Tools = append(c.Tools, name)
}

// RemoveTool strips `name` from Tools and Overrides. Returns true if anything
// was removed. Used by `silo unuse`.
func (c *ProjectConfig) RemoveTool(name string) bool {
	removed := false
	if len(c.Tools) > 0 {
		filtered := c.Tools[:0]
		for _, t := range c.Tools {
			if t == name {
				removed = true
				continue
			}
			filtered = append(filtered, t)
		}
		c.Tools = append([]string(nil), filtered...)
		if len(c.Tools) == 0 {
			c.Tools = nil
		}
	}
	if _, ok := c.Overrides[name]; ok {
		delete(c.Overrides, name)
		removed = true
	}
	c.cleanupEmpty()
	return removed
}

// SetOverrideImage records an image override for `tool`. Creates the override
// entry if missing; leaves other override fields untouched.
func (c *ProjectConfig) SetOverrideImage(tool, image string) {
	o := c.ensureToolOverride(tool)
	o.Image = image
	c.setToolOverride(tool, *o)
}

// AddPort adds a host:guest forwarding rule for `tool`. No-op if already present.
func (c *ProjectConfig) AddPort(tool string, host, guest uint16) {
	o := c.ensureToolOverride(tool)
	for _, p := range o.Ports {
		if p.Host == host && p.Guest == guest {
			return
		}
	}
	o.Ports = append(o.Ports, PortMapping{Host: host, Guest: guest})
	c.setToolOverride(tool, *o)
}

// RemovePort drops a host:guest rule. Returns true if something was removed.
func (c *ProjectConfig) RemovePort(tool string, host, guest uint16) bool {
	o, ok := c.Overrides[tool]
	if !ok {
		return false
	}
	filtered := o.Ports[:0]
	removed := false
	for _, p := range o.Ports {
		if p.Host == host && p.Guest == guest {
			removed = true
			continue
		}
		filtered = append(filtered, p)
	}
	if !removed {
		return false
	}
	o.Ports = filtered
	c.Overrides[tool] = o
	c.cleanupEmpty()
	return true
}

// AddNetworkAllow adds a domain to the proxy allowlist for `tool`.
func (c *ProjectConfig) AddNetworkAllow(tool, domain string) {
	o := c.ensureToolOverride(tool)
	if o.Network == nil {
		o.Network = &NetworkConfig{HostAccess: true}
	}
	if o.Network.Proxy == nil {
		o.Network.Proxy = &ProxyConfig{}
	}
	for _, d := range o.Network.Proxy.Allow {
		if d == domain {
			c.setToolOverride(tool, *o)
			return
		}
	}
	o.Network.Proxy.Allow = append(o.Network.Proxy.Allow, domain)
	c.setToolOverride(tool, *o)
}

// AddNetworkDeny adds a domain to the proxy denylist for `tool`.
func (c *ProjectConfig) AddNetworkDeny(tool, domain string) {
	o := c.ensureToolOverride(tool)
	if o.Network == nil {
		o.Network = &NetworkConfig{HostAccess: true}
	}
	if o.Network.Proxy == nil {
		o.Network.Proxy = &ProxyConfig{}
	}
	for _, d := range o.Network.Proxy.Deny {
		if d == domain {
			c.setToolOverride(tool, *o)
			return
		}
	}
	o.Network.Proxy.Deny = append(o.Network.Proxy.Deny, domain)
	c.setToolOverride(tool, *o)
}

// RemoveNetworkDomain drops `domain` from both allow and deny. Returns true if removed.
func (c *ProjectConfig) RemoveNetworkDomain(tool, domain string) bool {
	o, ok := c.Overrides[tool]
	if !ok || o.Network == nil || o.Network.Proxy == nil {
		return false
	}
	removed := false
	if filtered, ok := filterOut(o.Network.Proxy.Allow, domain); ok {
		o.Network.Proxy.Allow = filtered
		removed = true
	}
	if filtered, ok := filterOut(o.Network.Proxy.Deny, domain); ok {
		o.Network.Proxy.Deny = filtered
		removed = true
	}
	if removed {
		c.Overrides[tool] = o
		c.cleanupEmpty()
	}
	return removed
}

// cleanupEmpty removes empty nested structures so saved YAML is tidy.
func (c *ProjectConfig) cleanupEmpty() {
	if c.Overrides == nil {
		return
	}
	for tool, o := range c.Overrides {
		if len(o.Ports) == 0 {
			o.Ports = nil
		}
		if o.Network != nil {
			if o.Network.Proxy != nil {
				if len(o.Network.Proxy.Deny) == 0 {
					o.Network.Proxy.Deny = nil
				}
				if len(o.Network.Proxy.Allow) == 0 && o.Network.Proxy.Deny == nil {
					o.Network.Proxy = nil
				}
			}
			if !o.Network.HostAccess && o.Network.Proxy == nil {
				o.Network = nil
			}
		}
		if len(o.PostInstall) == 0 {
			o.PostInstall = nil
		}
		if len(o.Cache) == 0 {
			o.Cache = nil
		}
		if o.Image == "" && len(o.Env) == 0 && o.Network == nil && len(o.Ports) == 0 && len(o.PostInstall) == 0 && len(o.Cache) == 0 && o.CPUs == 0 && o.MemoryMB == 0 && o.RootfsSizeMB == 0 && o.Workdir == "" && len(o.PassEnv) == 0 && o.LSP == nil {
			delete(c.Overrides, tool)
			continue
		}
		c.Overrides[tool] = o
	}
	if len(c.Overrides) == 0 {
		c.Overrides = nil
	}
}

// filterOut returns (filtered, true) if `v` was present in `s`, else (s, false).
func filterOut(s []string, v string) ([]string, bool) {
	out := s[:0]
	removed := false
	for _, x := range s {
		if x == v {
			removed = true
			continue
		}
		out = append(out, x)
	}
	if !removed {
		return s, false
	}
	// Reallocate a fresh slice so the backing array isn't shared.
	return append([]string(nil), out...), true
}

// mergeLspConfig returns base merged with overlay. nil overlay returns base.
// nil base returns a deep copy of overlay. Non-empty overlay scalar/array fields
// win; overlay env merges per-key onto base; overlay cache mounts dedup-by-guest
// onto base via mergeCacheMounts. The result is always a fresh allocation —
// neither input is mutated and the returned pointers are not shared.
func mergeLspConfig(base, overlay *LspConfig) *LspConfig {
	if overlay == nil {
		return base
	}
	out := LspConfig{}
	if base != nil {
		out.Command = append([]string(nil), base.Command...)
		out.Install = base.Install
		out.Cache = append([]CacheMount(nil), base.Cache...)
		if len(base.Env) > 0 {
			out.Env = make(map[string]string, len(base.Env))
			for k, v := range base.Env {
				out.Env[k] = v
			}
		}
	}
	if len(overlay.Command) > 0 {
		out.Command = append([]string(nil), overlay.Command...)
	}
	if overlay.Install != "" {
		out.Install = overlay.Install
	}
	if len(overlay.Cache) > 0 {
		out.Cache = mergeCacheMounts(out.Cache, overlay.Cache)
	}
	if len(overlay.Env) > 0 {
		if out.Env == nil {
			out.Env = make(map[string]string, len(overlay.Env))
		}
		for k, v := range overlay.Env {
			out.Env[k] = v
		}
	}
	return &out
}

// mergeCacheMounts returns base+overlay, deduplicated by Guest path.
// Overlay wins on conflict. Order: base first, then overlay entries that
// weren't already in base.
func mergeCacheMounts(base, overlay []CacheMount) []CacheMount {
	if len(base) == 0 && len(overlay) == 0 {
		return nil
	}
	index := make(map[string]int, len(base)+len(overlay))
	out := make([]CacheMount, 0, len(base)+len(overlay))
	for _, m := range base {
		index[m.Guest] = len(out)
		out = append(out, m)
	}
	for _, m := range overlay {
		if idx, ok := index[m.Guest]; ok {
			out[idx] = m
			continue
		}
		index[m.Guest] = len(out)
		out = append(out, m)
	}
	return out
}

// dedupMerge returns base || overlay, order-preserving. nil on empty.
func dedupMerge(base, overlay []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(base)+len(overlay))
	for _, s := range base {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	for _, s := range overlay {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
