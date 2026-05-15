// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"errors"
	"strings"
	"testing"

	"github.com/rchekalov/silo/internal/config"
	"github.com/rchekalov/silo/internal/errs"
)

func newCfg(tools map[string]config.ToolDefinition) *config.GlobalConfig {
	return &config.GlobalConfig{Version: 1, Tools: tools}
}

func TestResolveToolOrShim_DirectToolWins(t *testing.T) {
	// A tool named "foo" and another tool ("bar") exposing "foo" as a shim.
	// Direct tool lookup should win; no implicit --shim should be set.
	cfg := newCfg(map[string]config.ToolDefinition{
		"foo": {Image: "img/foo"},
		"bar": {
			Image: "img/bar",
			Shims: []config.ShimMapping{{HostCommand: "foo", ContainerCommand: "foo"}},
		},
	})

	name, def, shim, err := resolveToolOrShim(cfg, "foo")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if name != "foo" || def.Image != "img/foo" {
		t.Fatalf("tool mismatch: name=%s image=%s", name, def.Image)
	}
	if shim != "" {
		t.Fatalf("direct tool match must not set shim, got %q", shim)
	}
}

func TestResolveToolOrShim_ShimFallback(t *testing.T) {
	cfg := newCfg(map[string]config.ToolDefinition{
		"claude-code": {
			Image: "docker.io/library/node:22-slim",
			Shims: []config.ShimMapping{{HostCommand: "claude", ContainerCommand: "claude"}},
		},
	})

	name, def, shim, err := resolveToolOrShim(cfg, "claude")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if name != "claude-code" || def.Image == "" {
		t.Fatalf("tool mismatch: %s / %+v", name, def)
	}
	if shim != "claude" {
		t.Fatalf("shim mismatch: %q", shim)
	}
}

func TestResolveToolOrShim_Ambiguous(t *testing.T) {
	cfg := newCfg(map[string]config.ToolDefinition{
		"python": {
			Image: "docker.io/library/python:3.12-slim",
			Shims: []config.ShimMapping{{HostCommand: "py", ContainerCommand: "python"}},
		},
		"python2": {
			Image: "docker.io/library/python:2.7-slim",
			Shims: []config.ShimMapping{{HostCommand: "py", ContainerCommand: "python"}},
		},
	})

	_, _, _, err := resolveToolOrShim(cfg, "py")
	if err == nil {
		t.Fatal("expected ambiguity error")
	}
	if !errors.Is(err, errs.ErrConfig) {
		t.Fatalf("want ErrConfig, got %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "python") || !strings.Contains(msg, "python2") {
		t.Fatalf("error should list both tools: %s", msg)
	}
}

func TestResolveToolOrShim_NotFound(t *testing.T) {
	cfg := newCfg(map[string]config.ToolDefinition{
		"python": {Image: "img"},
	})

	_, _, _, err := resolveToolOrShim(cfg, "nope")
	if !errors.Is(err, errs.ErrToolNotInstalled) {
		t.Fatalf("want ErrToolNotInstalled, got %v", err)
	}
}

// Stored python config from a pre-uv-mount install: only the pip cache mount.
// resolveToolOrShim should overlay the registry's uv + poetry mounts on top so
// users on older configs pick up new caching without `silo install --force`.
func TestResolveToolOrShim_OverlayCacheMountsFromRegistry(t *testing.T) {
	cfg := newCfg(map[string]config.ToolDefinition{
		"python": {
			Image: "docker.io/library/python:3.14-slim",
			Cache: []config.CacheMount{
				{Guest: "/root/.cache/pip", Host: "~/.silo/cache/python/pip"},
			},
		},
	})

	_, def, _, err := resolveToolOrShim(cfg, "python")
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	guestPaths := map[string]bool{}
	for _, m := range def.Cache {
		guestPaths[m.Guest] = true
	}
	for _, want := range []string{"/root/.cache/pip", "/root/.cache/uv", "/root/.cache/pypoetry"} {
		if !guestPaths[want] {
			t.Errorf("expected cache mount for %s after overlay; got mounts: %+v", want, def.Cache)
		}
	}
}

// A user who deliberately removed and replaced a registry cache mount with a
// custom host path should NOT have the registry version layered back on top.
// Dedup key is the guest path; stored entries win on conflict.
func TestResolveToolOrShim_OverlayPreservesCustomCacheHost(t *testing.T) {
	cfg := newCfg(map[string]config.ToolDefinition{
		"python": {
			Image: "docker.io/library/python:3.14-slim",
			Cache: []config.CacheMount{
				{Guest: "/root/.cache/pip", Host: "/custom/pip-cache"},
			},
		},
	})

	_, def, _, err := resolveToolOrShim(cfg, "python")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	for _, m := range def.Cache {
		if m.Guest == "/root/.cache/pip" && m.Host != "/custom/pip-cache" {
			t.Errorf("custom pip host clobbered by registry overlay: got %q", m.Host)
		}
	}
}
