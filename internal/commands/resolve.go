// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"strings"

	"github.com/rchekalov/silo/internal/config"
	"github.com/rchekalov/silo/internal/errs"
	"github.com/rchekalov/silo/internal/tools"
)

// resolveToolOrShim looks up `typed` first as a tool name and, if that fails,
// as a shim belonging to exactly one installed tool. On shim resolution the
// returned shimName is the originally typed name so callers can implicitly
// set --shim behavior (e.g. `silo run claude` -> claude-code + --shim claude).
// When multiple tools claim the same shim, an ErrConfig is returned listing
// the candidates.
//
// The returned ToolDefinition has registry additions overlaid on top of the
// stored config so users on older installations automatically pick up new
// registry fields (network/proxy, cache mounts) after a binary upgrade —
// without an explicit `silo install <tool>` re-run. Stored fields are
// preserved when present; the overlay only fills gaps.
func resolveToolOrShim(cfg *config.GlobalConfig, typed string) (string, config.ToolDefinition, string, error) {
	name, def, shim, err := resolveStored(cfg, typed)
	if err != nil {
		return name, def, shim, err
	}
	def = overlayRegistryNetwork(name, def)
	def = overlayRegistryCacheMounts(name, def)
	return name, def, shim, nil
}

func resolveStored(cfg *config.GlobalConfig, typed string) (string, config.ToolDefinition, string, error) {
	if def, ok := cfg.Tools[typed]; ok {
		return typed, def, "", nil
	}
	matches := cfg.ResolveShimAll(typed)
	switch len(matches) {
	case 0:
		return "", config.ToolDefinition{}, "", errs.ToolNotInstalledError(typed)
	case 1:
		return matches[0], cfg.Tools[matches[0]], typed, nil
	default:
		return "", config.ToolDefinition{}, "", errs.Configf(
			"shim %q is claimed by multiple tools: %s; run `silo run <tool> --shim %s -- ...`",
			typed, strings.Join(matches, ", "), typed,
		)
	}
}

func overlayRegistryNetwork(name string, def config.ToolDefinition) config.ToolDefinition {
	if def.Network != nil && def.Network.Proxy != nil {
		// Stored config already has an allowlist — trust it as the source of
		// truth. The user (or a previous `silo install` from this binary)
		// authored it.
		return def
	}
	entry, ok, err := tools.LookupEntry(name)
	if err != nil || !ok || entry.Network == nil {
		return def
	}
	// Migration overlay: stored config predates 0.6 (no network block, or
	// only HostAccess). Pull the registry's network/proxy on top so the
	// deny-by-default policy and built-in allowlists kick in without an
	// explicit re-install.
	def.Network = cloneNetwork(entry.Network)
	return def
}

// overlayRegistryCacheMounts unions the registry's cache mounts with the
// stored config, deduped by guest path (stored entries win). This lets a
// registry-added mount (e.g. ~/.cache/uv) reach existing installs after a
// binary upgrade — the user gets the new caching benefit without re-running
// `silo install`. Mounts the user explicitly removed and replaced are still
// honored because the dedup is overlay-loses-on-conflict.
func overlayRegistryCacheMounts(name string, def config.ToolDefinition) config.ToolDefinition {
	entry, ok, err := tools.LookupEntry(name)
	if err != nil || !ok || len(entry.Cache) == 0 {
		return def
	}
	have := make(map[string]struct{}, len(def.Cache))
	for _, m := range def.Cache {
		have[m.Guest] = struct{}{}
	}
	for _, m := range entry.Cache {
		if _, present := have[m.Guest]; present {
			continue
		}
		def.Cache = append(def.Cache, m)
	}
	return def
}

// cloneNetwork is duplicated from internal/tools (unexported there) to keep
// the migration overlay package-local. Drop this when the in-tools helper
// becomes exported.
func cloneNetwork(n *config.NetworkConfig) *config.NetworkConfig {
	if n == nil {
		return nil
	}
	out := *n
	if n.Proxy != nil {
		p := *n.Proxy
		p.Allow = append([]string(nil), n.Proxy.Allow...)
		p.Deny = append([]string(nil), n.Proxy.Deny...)
		p.InstallAllow = append([]string(nil), n.Proxy.InstallAllow...)
		out.Proxy = &p
	}
	return &out
}
