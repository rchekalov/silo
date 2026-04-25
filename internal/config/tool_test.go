// SPDX-License-Identifier: Apache-2.0

package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestShimMappingParseSimple(t *testing.T) {
	m := ParseShim("python")
	if m.HostCommand != "python" || m.ContainerCommand != "python" {
		t.Fatalf("unexpected: %+v", m)
	}
}

func TestShimMappingParseCustom(t *testing.T) {
	m := ParseShim("npm2:npm")
	if m.HostCommand != "npm2" || m.ContainerCommand != "npm" {
		t.Fatalf("unexpected: %+v", m)
	}
}

func TestShimMappingString(t *testing.T) {
	cases := map[string]string{
		"python":   "python",
		"npm2:npm": "npm2:npm",
	}
	for spec, want := range cases {
		if got := ParseShim(spec).String(); got != want {
			t.Fatalf("%s: want %q got %q", spec, want, got)
		}
	}
}

func TestShimMappingYAMLRoundtrip(t *testing.T) {
	in := ParseShim("pip3")
	out, err := yaml.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var back ShimMapping
	if err := yaml.Unmarshal(out, &back); err != nil {
		t.Fatal(err)
	}
	if back != in {
		t.Fatalf("roundtrip mismatch: %+v vs %+v", in, back)
	}
}

func TestApplyOverrideImage(t *testing.T) {
	def := ToolDefinition{Image: "a:1", RootfsSizeMB: 2048}
	out := ApplyOverride(def, ToolOverride{Image: "b:2"})
	if out.Image != "b:2" {
		t.Fatalf("image %q", out.Image)
	}
	// unrelated fields preserved
	if out.RootfsSizeMB != 2048 {
		t.Fatalf("rootfsSize %d", out.RootfsSizeMB)
	}
	// original unmodified
	if def.Image != "a:1" {
		t.Fatalf("original mutated: %q", def.Image)
	}
}

func TestApplyOverrideEnvMerges(t *testing.T) {
	def := ToolDefinition{Env: map[string]string{"A": "1", "B": "2"}}
	out := ApplyOverride(def, ToolOverride{Env: map[string]string{"B": "22", "C": "3"}})
	if out.Env["A"] != "1" || out.Env["B"] != "22" || out.Env["C"] != "3" {
		t.Fatalf("env %+v", out.Env)
	}
	// base map untouched
	if def.Env["B"] != "2" {
		t.Fatalf("base env mutated: %+v", def.Env)
	}
}

func TestApplyOverrideNetworkReplaces(t *testing.T) {
	def := ToolDefinition{Network: &NetworkConfig{HostAccess: false}}
	override := ToolOverride{Network: &NetworkConfig{
		HostAccess: true,
		Proxy:      &ProxyConfig{Allow: []string{"*.github.com"}},
	}}
	out := ApplyOverride(def, override)
	if !out.Network.HostAccess {
		t.Fatalf("expected hostAccess true")
	}
	if got := out.Network.Proxy.Allow; len(got) != 1 || got[0] != "*.github.com" {
		t.Fatalf("allow %+v", got)
	}
	// mutating override's proxy list must not leak into out
	override.Network.Proxy.Allow[0] = "evil.com"
	if out.Network.Proxy.Allow[0] == "evil.com" {
		t.Fatalf("override's proxy list shared with result")
	}
}

func TestApplyOverridePortsReplace(t *testing.T) {
	def := ToolDefinition{Ports: []PortMapping{{Host: 1, Guest: 1}}}
	out := ApplyOverride(def, ToolOverride{Ports: []PortMapping{{Host: 2, Guest: 2}}})
	if len(out.Ports) != 1 || out.Ports[0].Host != 2 {
		t.Fatalf("ports %+v", out.Ports)
	}
}

func TestApplyOverrideAppendsPostInstall(t *testing.T) {
	def := ToolDefinition{PostInstall: []string{"apt-get install base"}}
	out := ApplyOverride(def, ToolOverride{PostInstall: []string{"apt-get install kotlin"}})
	if len(out.PostInstall) != 2 {
		t.Fatalf("postInstall %+v", out.PostInstall)
	}
	if out.PostInstall[0] != "apt-get install base" || out.PostInstall[1] != "apt-get install kotlin" {
		t.Fatalf("postInstall order %+v", out.PostInstall)
	}
	// Original def untouched.
	if len(def.PostInstall) != 1 {
		t.Fatalf("def mutated: %+v", def.PostInstall)
	}
}

func TestApplyOverrideCacheDedupesByGuest(t *testing.T) {
	def := ToolDefinition{
		Cache: []CacheMount{
			{Guest: "/root/.npm", Host: "~/.silo/cache/node/npm"},
			{Guest: "/root/.claude", Host: "~/.silo/cache/claude-code/config", NoGC: true},
		},
	}
	override := ToolOverride{
		Cache: []CacheMount{
			{Guest: "/root/.npm", Host: "~/custom/npm"}, // replaces
			{Guest: "/root/.gradle", Host: "~/.silo/cache/claude-code/gradle"},
		},
	}
	out := ApplyOverride(def, override)
	if len(out.Cache) != 3 {
		t.Fatalf("cache len=%d %+v", len(out.Cache), out.Cache)
	}
	if out.Cache[0].Host != "~/custom/npm" {
		t.Fatalf("cache[0] not replaced: %+v", out.Cache[0])
	}
	if !out.Cache[1].NoGC {
		t.Fatalf("cache[1] noGC lost: %+v", out.Cache[1])
	}
	if out.Cache[2].Guest != "/root/.gradle" {
		t.Fatalf("cache[2] not appended: %+v", out.Cache[2])
	}
}

func TestApplyOverrideEmptyPostInstallKeepsBase(t *testing.T) {
	def := ToolDefinition{PostInstall: []string{"registry step"}}
	out := ApplyOverride(def, ToolOverride{})
	if len(out.PostInstall) != 1 || out.PostInstall[0] != "registry step" {
		t.Fatalf("postInstall %+v", out.PostInstall)
	}
}

func TestApplyOverrideResourcesReplace(t *testing.T) {
	def := ToolDefinition{CPUs: 2, MemoryMB: 512, RootfsSizeMB: 2048}
	out := ApplyOverride(def, ToolOverride{CPUs: 4, MemoryMB: 6144, RootfsSizeMB: 4096})
	if out.CPUs != 4 || out.MemoryMB != 6144 || out.RootfsSizeMB != 4096 {
		t.Fatalf("resources not applied: %+v", out)
	}
	// Original untouched.
	if def.CPUs != 2 || def.MemoryMB != 512 || def.RootfsSizeMB != 2048 {
		t.Fatalf("base mutated: %+v", def)
	}
}

func TestApplyOverrideZeroResourcesKeepsBase(t *testing.T) {
	def := ToolDefinition{CPUs: 2, MemoryMB: 512, RootfsSizeMB: 2048}
	out := ApplyOverride(def, ToolOverride{Image: "x:1"})
	if out.CPUs != 2 || out.MemoryMB != 512 || out.RootfsSizeMB != 2048 {
		t.Fatalf("zero-override should not clobber base: %+v", out)
	}
}

func TestApplyOverridePartialResources(t *testing.T) {
	def := ToolDefinition{CPUs: 2, MemoryMB: 512, RootfsSizeMB: 2048}
	out := ApplyOverride(def, ToolOverride{MemoryMB: 6144})
	if out.MemoryMB != 6144 {
		t.Fatalf("MemoryMB not overridden: %+v", out)
	}
	if out.CPUs != 2 || out.RootfsSizeMB != 2048 {
		t.Fatalf("untouched resource fields drifted: %+v", out)
	}
}

func TestApplyOverrideWorkdirReplaces(t *testing.T) {
	def := ToolDefinition{Workdir: "/workspace"}
	out := ApplyOverride(def, ToolOverride{Workdir: "/app"})
	if out.Workdir != "/app" {
		t.Fatalf("workdir not overridden: %q", out.Workdir)
	}
}

func TestApplyOverrideEmptyWorkdirKeepsBase(t *testing.T) {
	def := ToolDefinition{Workdir: "/workspace"}
	out := ApplyOverride(def, ToolOverride{Image: "x:1"})
	if out.Workdir != "/workspace" {
		t.Fatalf("workdir clobbered by empty override: %q", out.Workdir)
	}
}

func TestApplyOverridePassEnvDedupAppends(t *testing.T) {
	def := ToolDefinition{PassEnv: []string{"GITHUB_TOKEN", "AWS_PROFILE"}}
	out := ApplyOverride(def, ToolOverride{PassEnv: []string{"AWS_PROFILE", "ANTHROPIC_API_KEY"}})
	want := []string{"GITHUB_TOKEN", "AWS_PROFILE", "ANTHROPIC_API_KEY"}
	if len(out.PassEnv) != len(want) {
		t.Fatalf("passEnv %+v want %+v", out.PassEnv, want)
	}
	for i, k := range want {
		if out.PassEnv[i] != k {
			t.Fatalf("passEnv[%d] = %q want %q (full %+v)", i, out.PassEnv[i], k, out.PassEnv)
		}
	}
	// Original untouched.
	if len(def.PassEnv) != 2 {
		t.Fatalf("base passEnv mutated: %+v", def.PassEnv)
	}
}

func TestApplyOverrideLspMergesEnvAndCache(t *testing.T) {
	def := ToolDefinition{
		LSP: &LspConfig{
			Command: []string{"pyright-langserver", "--stdio"},
			Install: "npm i -g pyright",
			Env:     map[string]string{"NODE_OPTIONS": "--max-old-space-size=2048"},
			Cache:   []CacheMount{{Guest: "/root/.cache/pyright", Host: "~/.silo/cache/python/pyright"}},
		},
	}
	override := ToolOverride{
		LSP: &LspConfig{
			Install: "npm i -g pyright@1.1.350",
			Env:     map[string]string{"NODE_OPTIONS": "--max-old-space-size=4096", "PYRIGHT_LOG": "verbose"},
			Cache:   []CacheMount{{Guest: "/root/.cache/pyright", Host: "~/custom/pyright-cache"}},
		},
	}
	out := ApplyOverride(def, override)
	if out.LSP == nil {
		t.Fatal("lsp dropped")
	}
	// Command unchanged because override didn't set it.
	if len(out.LSP.Command) != 2 || out.LSP.Command[0] != "pyright-langserver" {
		t.Fatalf("command lost: %+v", out.LSP.Command)
	}
	if out.LSP.Install != "npm i -g pyright@1.1.350" {
		t.Fatalf("install not overridden: %q", out.LSP.Install)
	}
	if out.LSP.Env["NODE_OPTIONS"] != "--max-old-space-size=4096" || out.LSP.Env["PYRIGHT_LOG"] != "verbose" {
		t.Fatalf("env merge wrong: %+v", out.LSP.Env)
	}
	// Cache deduped by guest path; override host wins.
	if len(out.LSP.Cache) != 1 || out.LSP.Cache[0].Host != "~/custom/pyright-cache" {
		t.Fatalf("cache merge wrong: %+v", out.LSP.Cache)
	}
	// Base untouched.
	if def.LSP.Install != "npm i -g pyright" {
		t.Fatalf("base lsp mutated: %+v", def.LSP)
	}
	if def.LSP.Env["NODE_OPTIONS"] != "--max-old-space-size=2048" {
		t.Fatalf("base lsp env mutated: %+v", def.LSP.Env)
	}
}

func TestApplyOverrideLspNilOverrideKeepsBase(t *testing.T) {
	def := ToolDefinition{LSP: &LspConfig{Install: "npm i -g pyright"}}
	out := ApplyOverride(def, ToolOverride{Image: "x:1"})
	if out.LSP == nil || out.LSP.Install != "npm i -g pyright" {
		t.Fatalf("base lsp dropped: %+v", out.LSP)
	}
}

func TestApplyOverrideLspOntoNilBase(t *testing.T) {
	def := ToolDefinition{} // no LSP at all on base
	out := ApplyOverride(def, ToolOverride{LSP: &LspConfig{
		Command: []string{"my-lsp"},
		Install: "echo nothing",
	}})
	if out.LSP == nil {
		t.Fatal("override-only lsp lost")
	}
	if len(out.LSP.Command) != 1 || out.LSP.Command[0] != "my-lsp" {
		t.Fatalf("command not applied: %+v", out.LSP.Command)
	}
	if out.LSP.Install != "echo nothing" {
		t.Fatalf("install not applied: %q", out.LSP.Install)
	}
}

func TestApplyOverrideEmptyIsIdentity(t *testing.T) {
	def := ToolDefinition{Image: "a:1", Env: map[string]string{"K": "V"}}
	out := ApplyOverride(def, ToolOverride{})
	if out.Image != def.Image || out.Env["K"] != "V" {
		t.Fatalf("identity failed: %+v", out)
	}
}

func TestToolDefinitionDefaults(t *testing.T) {
	var tool ToolDefinition
	if err := yaml.Unmarshal([]byte("image: docker.io/library/python:3.12-slim\n"), &tool); err != nil {
		t.Fatal(err)
	}
	tool.ApplyDefaults()
	if tool.Workdir != DefaultWorkdir {
		t.Fatalf("workdir default: %q", tool.Workdir)
	}
	if tool.CPUs != DefaultCPUs || tool.MemoryMB != DefaultMemoryMB || tool.RootfsSizeMB != DefaultRootfsSizeMB {
		t.Fatalf("unexpected defaults: %+v", tool)
	}
	if len(tool.Shims) != 0 || len(tool.Cache) != 0 || len(tool.Env) != 0 {
		t.Fatalf("expected empties: %+v", tool)
	}
}
