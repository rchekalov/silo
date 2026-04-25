// SPDX-License-Identifier: Apache-2.0

package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestParseProjectConfig(t *testing.T) {
	src := `
passEnv:
  - GITHUB_TOKEN
passFiles:
  - .npmrc
overrides:
  python:
    image: docker.io/library/python:3.11-slim
    env:
      PYTHONPATH: /workspace/src
`
	var c ProjectConfig
	if err := yaml.Unmarshal([]byte(src), &c); err != nil {
		t.Fatal(err)
	}
	if len(c.PassEnv) != 1 || c.PassEnv[0] != "GITHUB_TOKEN" {
		t.Fatalf("passEnv %+v", c.PassEnv)
	}
	if len(c.PassFiles) != 1 || c.PassFiles[0] != ".npmrc" {
		t.Fatalf("passFiles %+v", c.PassFiles)
	}
	py := c.Overrides["python"]
	if py.Image != "docker.io/library/python:3.11-slim" {
		t.Fatalf("image %q", py.Image)
	}
}

func TestAddPort(t *testing.T) {
	var c ProjectConfig
	c.AddPort("python", 8080, 8080)
	if p := c.Overrides["python"].Ports; len(p) != 1 || p[0].Host != 8080 {
		t.Fatalf("ports %+v", p)
	}
}

func TestAddPortNoDuplicate(t *testing.T) {
	var c ProjectConfig
	c.AddPort("python", 8080, 8080)
	c.AddPort("python", 8080, 8080)
	if p := c.Overrides["python"].Ports; len(p) != 1 {
		t.Fatalf("expected 1 port, got %d", len(p))
	}
}

func TestRemovePort(t *testing.T) {
	var c ProjectConfig
	c.AddPort("python", 8080, 8080)
	c.AddPort("python", 3000, 3000)
	if !c.RemovePort("python", 8080, 8080) {
		t.Fatal("remove should return true")
	}
	if p := c.Overrides["python"].Ports; len(p) != 1 || p[0].Host != 3000 {
		t.Fatalf("ports %+v", p)
	}
}

func TestRemoveLastPortCleansUp(t *testing.T) {
	var c ProjectConfig
	c.AddPort("python", 8080, 8080)
	if !c.RemovePort("python", 8080, 8080) {
		t.Fatal("remove should return true")
	}
	if c.Overrides != nil {
		t.Fatalf("overrides should be nil, got %+v", c.Overrides)
	}
}

func TestRemoveNonexistentPort(t *testing.T) {
	var c ProjectConfig
	if c.RemovePort("python", 8080, 8080) {
		t.Fatal("remove should return false")
	}
}

func TestAddNetworkAllow(t *testing.T) {
	var c ProjectConfig
	c.AddNetworkAllow("python", "*.github.com")
	net := c.Overrides["python"].Network
	if net == nil || !net.HostAccess {
		t.Fatalf("network %+v", net)
	}
	if got := net.Proxy.Allow; len(got) != 1 || got[0] != "*.github.com" {
		t.Fatalf("allow %+v", got)
	}
}

func TestAddNetworkDeny(t *testing.T) {
	var c ProjectConfig
	c.AddNetworkDeny("python", "evil.com")
	deny := c.Overrides["python"].Network.Proxy.Deny
	if len(deny) != 1 || deny[0] != "evil.com" {
		t.Fatalf("deny %+v", deny)
	}
}

func TestRemoveNetworkDomain(t *testing.T) {
	var c ProjectConfig
	c.AddNetworkAllow("python", "*.github.com")
	c.AddNetworkAllow("python", "pypi.org")
	if !c.RemoveNetworkDomain("python", "*.github.com") {
		t.Fatal("remove should return true")
	}
	allow := c.Overrides["python"].Network.Proxy.Allow
	if len(allow) != 1 || allow[0] != "pypi.org" {
		t.Fatalf("allow %+v", allow)
	}
}

func TestRemoveLastNetworkDomainCleansProxy(t *testing.T) {
	var c ProjectConfig
	c.AddNetworkAllow("python", "*.github.com")
	if !c.RemoveNetworkDomain("python", "*.github.com") {
		t.Fatal("remove should return true")
	}
	// hostAccess stays true, so Network is kept but Proxy is nil
	net := c.Overrides["python"].Network
	if net == nil || net.Proxy != nil {
		t.Fatalf("expected network with nil proxy, got %+v", net)
	}
}

func TestPortRoundtripYAML(t *testing.T) {
	var c ProjectConfig
	c.AddPort("node", 3000, 3000)
	c.AddNetworkAllow("node", "npmjs.org")
	out, err := yaml.Marshal(&c)
	if err != nil {
		t.Fatal(err)
	}
	var back ProjectConfig
	if err := yaml.Unmarshal(out, &back); err != nil {
		t.Fatal(err)
	}
	ports := back.Overrides["node"].Ports
	if len(ports) != 1 || ports[0].Host != 3000 {
		t.Fatalf("ports %+v", ports)
	}
	allow := back.Overrides["node"].Network.Proxy.Allow
	if len(allow) != 1 || allow[0] != "npmjs.org" {
		t.Fatalf("allow %+v", allow)
	}
}

func TestMergeConfigs(t *testing.T) {
	base := ProjectConfig{PassEnv: []string{"A", "B"}}
	overlay := ProjectConfig{PassEnv: []string{"B", "C"}, PassFiles: []string{"file.txt"}}
	merged := overlay.MergeOver(&base)
	want := []string{"A", "B", "C"}
	if len(merged.PassEnv) != len(want) {
		t.Fatalf("passEnv %+v", merged.PassEnv)
	}
	for i, v := range want {
		if merged.PassEnv[i] != v {
			t.Fatalf("passEnv[%d] = %q, want %q", i, merged.PassEnv[i], v)
		}
	}
	if len(merged.PassFiles) != 1 || merged.PassFiles[0] != "file.txt" {
		t.Fatalf("passFiles %+v", merged.PassFiles)
	}
}

func TestProjectToolsUnionsToolsAndOverrides(t *testing.T) {
	c := ProjectConfig{
		Tools: []string{"python", "node"},
		Overrides: map[string]ToolOverride{
			"node": {Image: "node:20"},
			"rust": {Image: "rust:1.80"},
		},
	}
	got := c.ProjectTools()
	want := []string{"node", "python", "rust"}
	if len(got) != len(want) {
		t.Fatalf("ProjectTools %+v, want %+v", got, want)
	}
	for i, v := range want {
		if got[i] != v {
			t.Fatalf("ProjectTools[%d] = %q, want %q", i, got[i], v)
		}
	}
}

func TestProjectToolsEmpty(t *testing.T) {
	var c ProjectConfig
	if got := c.ProjectTools(); got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

func TestProjectToolsNilReceiver(t *testing.T) {
	var c *ProjectConfig
	if got := c.ProjectTools(); got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

func TestMergeOverTools(t *testing.T) {
	base := ProjectConfig{Tools: []string{"python", "node"}}
	overlay := ProjectConfig{Tools: []string{"node", "rust"}}
	merged := overlay.MergeOver(&base)
	want := []string{"python", "node", "rust"}
	if len(merged.Tools) != len(want) {
		t.Fatalf("Tools %+v, want %+v", merged.Tools, want)
	}
	for i, v := range want {
		if merged.Tools[i] != v {
			t.Fatalf("Tools[%d] = %q, want %q", i, merged.Tools[i], v)
		}
	}
}

func TestAddToolIdempotent(t *testing.T) {
	c := ProjectConfig{}
	c.AddTool("python")
	c.AddTool("python")
	c.AddTool("node")
	if len(c.Tools) != 2 || c.Tools[0] != "python" || c.Tools[1] != "node" {
		t.Fatalf("Tools=%+v", c.Tools)
	}
}

func TestRemoveToolStripsFromBothSections(t *testing.T) {
	c := ProjectConfig{
		Tools: []string{"python", "node"},
		Overrides: map[string]ToolOverride{
			"python": {Image: "docker.io/library/python:3.11-slim"},
		},
	}
	if !c.RemoveTool("python") {
		t.Fatal("RemoveTool returned false")
	}
	if len(c.Tools) != 1 || c.Tools[0] != "node" {
		t.Fatalf("Tools=%+v", c.Tools)
	}
	if _, ok := c.Overrides["python"]; ok {
		t.Fatalf("override for python still present: %+v", c.Overrides)
	}
}

func TestRemoveToolMissing(t *testing.T) {
	c := ProjectConfig{Tools: []string{"python"}}
	if c.RemoveTool("rust") {
		t.Fatal("expected RemoveTool to return false for missing tool")
	}
}

func TestMergeOverAppendsPostInstall(t *testing.T) {
	base := ProjectConfig{
		Overrides: map[string]ToolOverride{
			"claude-code": {PostInstall: []string{"base-step"}},
		},
	}
	overlay := ProjectConfig{
		Overrides: map[string]ToolOverride{
			"claude-code": {PostInstall: []string{"overlay-step"}},
		},
	}
	merged := overlay.MergeOver(&base)
	got := merged.Overrides["claude-code"].PostInstall
	if len(got) != 2 || got[0] != "base-step" || got[1] != "overlay-step" {
		t.Fatalf("postInstall %+v", got)
	}
}

func TestMergeOverDedupsCacheByGuest(t *testing.T) {
	base := ProjectConfig{
		Overrides: map[string]ToolOverride{
			"claude-code": {
				Cache: []CacheMount{{Guest: "/root/.npm", Host: "~/.silo/cache/node/npm"}},
			},
		},
	}
	overlay := ProjectConfig{
		Overrides: map[string]ToolOverride{
			"claude-code": {
				Cache: []CacheMount{
					{Guest: "/root/.npm", Host: "~/custom/npm"}, // replaces base entry
					{Guest: "/root/.gradle", Host: "~/.silo/cache/claude-code/gradle"},
				},
			},
		},
	}
	merged := overlay.MergeOver(&base)
	got := merged.Overrides["claude-code"].Cache
	if len(got) != 2 {
		t.Fatalf("cache len=%d %+v", len(got), got)
	}
	// First entry is the replaced /root/.npm with overlay's host path.
	if got[0].Guest != "/root/.npm" || got[0].Host != "~/custom/npm" {
		t.Fatalf("cache[0] not replaced: %+v", got[0])
	}
	if got[1].Guest != "/root/.gradle" {
		t.Fatalf("cache[1] not appended: %+v", got[1])
	}
}

func TestCleanupEmptyKeepsOverrideWithPostInstall(t *testing.T) {
	c := ProjectConfig{
		Overrides: map[string]ToolOverride{
			"claude-code": {PostInstall: []string{"apt-get install -y kotlin"}},
		},
	}
	// Round-trip via MergeOver which calls cleanupEmpty indirectly; instead
	// call directly through RemovePort which triggers cleanupEmpty unconditionally.
	c.RemovePort("claude-code", 9999, 9999)
	if _, ok := c.Overrides["claude-code"]; !ok {
		t.Fatalf("override dropped despite non-empty postInstall: %+v", c.Overrides)
	}
}

// TestToolOverrideParsesResources is the regression test for the bug where
// .siloconf entries like `cpus: 4 / memoryMB: 6144 / rootfsSizeMB: 4096` were
// silently dropped at parse time because ToolOverride lacked the fields.
// `silo current node` then reported the global default and the VM booted with
// the smaller cap, OOM-killing larger workloads with no diagnostic.
func TestToolOverrideParsesResources(t *testing.T) {
	src := `
tools: [node]
overrides:
  node:
    cpus: 4
    memoryMB: 6144
    rootfsSizeMB: 4096
`
	var c ProjectConfig
	if err := yaml.Unmarshal([]byte(src), &c); err != nil {
		t.Fatal(err)
	}
	o, ok := c.Overrides["node"]
	if !ok {
		t.Fatalf("override missing: %+v", c.Overrides)
	}
	if o.CPUs != 4 || o.MemoryMB != 6144 || o.RootfsSizeMB != 4096 {
		t.Fatalf("resources not parsed: %+v", o)
	}
}

func TestMergeOverResourceOverridesWin(t *testing.T) {
	base := ProjectConfig{
		Overrides: map[string]ToolOverride{
			"node": {CPUs: 2, MemoryMB: 1024, RootfsSizeMB: 2048},
		},
	}
	overlay := ProjectConfig{
		Overrides: map[string]ToolOverride{
			"node": {CPUs: 4, MemoryMB: 6144, RootfsSizeMB: 4096},
		},
	}
	merged := overlay.MergeOver(&base)
	got := merged.Overrides["node"]
	if got.CPUs != 4 || got.MemoryMB != 6144 || got.RootfsSizeMB != 4096 {
		t.Fatalf("overlay should win: %+v", got)
	}
}

func TestMergeOverZeroResourceKeepsBase(t *testing.T) {
	base := ProjectConfig{
		Overrides: map[string]ToolOverride{
			"node": {MemoryMB: 1024},
		},
	}
	// Overlay touches the same tool for an unrelated reason (env tweak) and
	// leaves the resource fields at zero. Base values must survive.
	overlay := ProjectConfig{
		Overrides: map[string]ToolOverride{
			"node": {Env: map[string]string{"NODE_ENV": "development"}},
		},
	}
	merged := overlay.MergeOver(&base)
	got := merged.Overrides["node"]
	if got.MemoryMB != 1024 {
		t.Fatalf("base MemoryMB lost: %+v", got)
	}
	if got.Env["NODE_ENV"] != "development" {
		t.Fatalf("overlay env lost: %+v", got)
	}
}

func TestCleanupEmptyKeepsOverrideWithResources(t *testing.T) {
	c := ProjectConfig{
		Overrides: map[string]ToolOverride{
			"node": {MemoryMB: 6144},
		},
	}
	c.cleanupEmpty()
	if _, ok := c.Overrides["node"]; !ok {
		t.Fatalf("override dropped despite non-zero MemoryMB: %+v", c.Overrides)
	}
	if c.Overrides["node"].MemoryMB != 6144 {
		t.Fatalf("MemoryMB lost during cleanup: %+v", c.Overrides["node"])
	}
}

func TestToolOverrideParsesWorkdirPassEnvLsp(t *testing.T) {
	src := `
overrides:
  python:
    workdir: /app
    passEnv: [AWS_PROFILE, ANTHROPIC_API_KEY]
    lsp:
      command: [pyright-langserver, --stdio]
      install: npm i -g pyright@1.1.350
      env:
        PYRIGHT_LOG: verbose
      cache:
        - guest: /root/.cache/pyright
          host: ~/custom/pyright-cache
`
	var c ProjectConfig
	if err := yaml.Unmarshal([]byte(src), &c); err != nil {
		t.Fatal(err)
	}
	o, ok := c.Overrides["python"]
	if !ok {
		t.Fatalf("override missing: %+v", c.Overrides)
	}
	if o.Workdir != "/app" {
		t.Fatalf("workdir not parsed: %q", o.Workdir)
	}
	if len(o.PassEnv) != 2 || o.PassEnv[0] != "AWS_PROFILE" || o.PassEnv[1] != "ANTHROPIC_API_KEY" {
		t.Fatalf("passEnv not parsed: %+v", o.PassEnv)
	}
	if o.LSP == nil {
		t.Fatal("lsp not parsed")
	}
	if o.LSP.Install != "npm i -g pyright@1.1.350" {
		t.Fatalf("lsp.install not parsed: %q", o.LSP.Install)
	}
	if o.LSP.Env["PYRIGHT_LOG"] != "verbose" {
		t.Fatalf("lsp.env not parsed: %+v", o.LSP.Env)
	}
	if len(o.LSP.Cache) != 1 || o.LSP.Cache[0].Host != "~/custom/pyright-cache" {
		t.Fatalf("lsp.cache not parsed: %+v", o.LSP.Cache)
	}
}

func TestMergeOverWorkdirAndPassEnv(t *testing.T) {
	base := ProjectConfig{
		Overrides: map[string]ToolOverride{
			"python": {Workdir: "/workspace", PassEnv: []string{"GITHUB_TOKEN"}},
		},
	}
	overlay := ProjectConfig{
		Overrides: map[string]ToolOverride{
			"python": {Workdir: "/app", PassEnv: []string{"GITHUB_TOKEN", "AWS_PROFILE"}},
		},
	}
	merged := overlay.MergeOver(&base)
	got := merged.Overrides["python"]
	if got.Workdir != "/app" {
		t.Fatalf("workdir overlay should win: %q", got.Workdir)
	}
	want := []string{"GITHUB_TOKEN", "AWS_PROFILE"}
	if len(got.PassEnv) != len(want) {
		t.Fatalf("passEnv %+v want %+v", got.PassEnv, want)
	}
	for i, k := range want {
		if got.PassEnv[i] != k {
			t.Fatalf("passEnv[%d] = %q want %q", i, got.PassEnv[i], k)
		}
	}
}

func TestMergeOverLspMergesAtSameTool(t *testing.T) {
	base := ProjectConfig{
		Overrides: map[string]ToolOverride{
			"python": {LSP: &LspConfig{Install: "npm i -g pyright", Env: map[string]string{"A": "1"}}},
		},
	}
	overlay := ProjectConfig{
		Overrides: map[string]ToolOverride{
			"python": {LSP: &LspConfig{Install: "npm i -g pyright@1.1.350", Env: map[string]string{"B": "2"}}},
		},
	}
	merged := overlay.MergeOver(&base)
	got := merged.Overrides["python"].LSP
	if got == nil {
		t.Fatal("lsp dropped")
	}
	if got.Install != "npm i -g pyright@1.1.350" {
		t.Fatalf("install: %q", got.Install)
	}
	if got.Env["A"] != "1" || got.Env["B"] != "2" {
		t.Fatalf("env merge: %+v", got.Env)
	}
}

func TestCleanupEmptyKeepsOverrideWithWorkdir(t *testing.T) {
	c := ProjectConfig{Overrides: map[string]ToolOverride{"python": {Workdir: "/app"}}}
	c.cleanupEmpty()
	if _, ok := c.Overrides["python"]; !ok {
		t.Fatalf("override dropped despite workdir: %+v", c.Overrides)
	}
}

func TestCleanupEmptyKeepsOverrideWithPassEnv(t *testing.T) {
	c := ProjectConfig{Overrides: map[string]ToolOverride{"python": {PassEnv: []string{"X"}}}}
	c.cleanupEmpty()
	if _, ok := c.Overrides["python"]; !ok {
		t.Fatalf("override dropped despite passEnv: %+v", c.Overrides)
	}
}

func TestCleanupEmptyKeepsOverrideWithLsp(t *testing.T) {
	c := ProjectConfig{Overrides: map[string]ToolOverride{"python": {LSP: &LspConfig{Install: "x"}}}}
	c.cleanupEmpty()
	if _, ok := c.Overrides["python"]; !ok {
		t.Fatalf("override dropped despite lsp: %+v", c.Overrides)
	}
}

func TestSetOverrideImage(t *testing.T) {
	c := ProjectConfig{}
	c.SetOverrideImage("python", "docker.io/library/python:3.11-slim")
	if c.Overrides == nil || c.Overrides["python"].Image != "docker.io/library/python:3.11-slim" {
		t.Fatalf("override not set: %+v", c.Overrides)
	}
	// Setting twice replaces (not appends) — idempotent on the image field.
	c.SetOverrideImage("python", "docker.io/library/python:3.12-slim")
	if c.Overrides["python"].Image != "docker.io/library/python:3.12-slim" {
		t.Fatalf("override image not replaced: %+v", c.Overrides["python"])
	}
}
