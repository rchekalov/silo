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
