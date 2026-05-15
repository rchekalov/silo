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
// The returned ToolDefinition has its Network field overlaid from the
// registry when the stored config has no network block. This lets users on
// older installations (pre-0.6) automatically pick up the registry's
// deny-by-default proxy allowlist after a binary upgrade — without it,
// `silo run python pip install …` would fail with "no network" until the
// user runs `silo install python` again.
func resolveToolOrShim(cfg *config.GlobalConfig, typed string) (string, config.ToolDefinition, string, error) {
	name, def, shim, err := resolveStored(cfg, typed)
	if err != nil {
		return name, def, shim, err
	}
	def = overlayRegistryNetwork(name, def)
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
