// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"path/filepath"
	"testing"

	"github.com/rchekalov/silo/internal/config"
)

func TestBuildToolCacheSpecsSkipsNoGC(t *testing.T) {
	global := &config.GlobalConfig{
		Tools: map[string]config.ToolDefinition{
			"claude-code": {
				Cache: []config.CacheMount{
					{Guest: "/root/.claude", Host: "~/.silo/cache/claude-code/config", NoGC: true},
					{Guest: "/root/.npm", Host: "~/.silo/cache/node/npm"},
				},
			},
		},
	}

	specs := buildToolCacheSpecs(global, config.ToolCachePolicy{})

	if got := len(specs); got != 1 {
		t.Fatalf("expected 1 GC spec (NoGC mount skipped), got %d: %+v", got, specs)
	}
	if specs[0].Tool != "claude-code" {
		t.Fatalf("unexpected tool: %+v", specs[0])
	}
	if base := filepath.Base(specs[0].HostPath); base != "npm" {
		t.Fatalf("expected the npm mount to remain (got base %q): %+v", base, specs[0])
	}
}

func TestBuildToolCacheSpecsAllNoGCReturnsEmpty(t *testing.T) {
	global := &config.GlobalConfig{
		Tools: map[string]config.ToolDefinition{
			"claude-code": {
				Cache: []config.CacheMount{
					{Guest: "/root/.claude", Host: "~/.silo/cache/claude-code/config", NoGC: true},
				},
			},
		},
	}
	if specs := buildToolCacheSpecs(global, config.ToolCachePolicy{}); len(specs) != 0 {
		t.Fatalf("expected 0 specs when every mount is noGC, got %d: %+v", len(specs), specs)
	}
}
