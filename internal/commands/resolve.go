// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"strings"

	"github.com/rchekalov/silo/internal/config"
	"github.com/rchekalov/silo/internal/errs"
)

// resolveToolOrShim looks up `typed` first as a tool name and, if that fails,
// as a shim belonging to exactly one installed tool. On shim resolution the
// returned shimName is the originally typed name so callers can implicitly
// set --shim behavior (e.g. `silo run claude` -> claude-code + --shim claude).
// When multiple tools claim the same shim, an ErrConfig is returned listing
// the candidates.
func resolveToolOrShim(cfg *config.GlobalConfig, typed string) (string, config.ToolDefinition, string, error) {
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
