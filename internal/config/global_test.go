// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestParseGlobalConfigYAML(t *testing.T) {
	src := `
version: 1
tools:
  python:
    image: docker.io/library/python:3.12-slim
    shims:
      - python
      - python3
      - pip
      - pip3
    cache:
      - guest: /root/.cache/pip
        host: ~/.silo/cache/python/pip
    workdir: /workspace
    env:
      PYTHONDONTWRITEBYTECODE: "1"
`
	var c GlobalConfig
	if err := yaml.Unmarshal([]byte(src), &c); err != nil {
		t.Fatal(err)
	}
	if c.Version != 1 {
		t.Fatalf("version %d", c.Version)
	}
	py, ok := c.Tools["python"]
	if !ok {
		t.Fatal("missing python")
	}
	if py.Image != "docker.io/library/python:3.12-slim" {
		t.Fatalf("image %q", py.Image)
	}
	if len(py.Shims) != 4 || py.Shims[0].HostCommand != "python" {
		t.Fatalf("shims %+v", py.Shims)
	}
	if len(py.Cache) != 1 || py.Cache[0].Guest != "/root/.cache/pip" {
		t.Fatalf("cache %+v", py.Cache)
	}
	if py.Env["PYTHONDONTWRITEBYTECODE"] != "1" {
		t.Fatalf("env %+v", py.Env)
	}
}

func TestResolveShim(t *testing.T) {
	src := `
version: 1
tools:
  python:
    image: docker.io/library/python:3.12-slim
    shims:
      - python
      - pip
`
	var c GlobalConfig
	if err := yaml.Unmarshal([]byte(src), &c); err != nil {
		t.Fatal(err)
	}
	name, tool := c.ResolveShim("pip")
	if name != "python" || tool == nil {
		t.Fatalf("got %q, %v", name, tool)
	}
	if n, _ := c.ResolveShim("node"); n != "" {
		t.Fatalf("expected empty, got %q", n)
	}
}

func TestLoadGlobalConfigMigratesV1ToV2(t *testing.T) {
	// A v1 file (no `pinnedGlobally` key, version: 1) must come back with
	// every tool flagged PinnedGlobally=true so legacy installs keep their
	// "silo always handles this" behavior after the upgrade.
	dir := t.TempDir()
	path := dir + "/config.yaml"
	src := `version: 1
tools:
  python:
    image: docker.io/library/python:3.12-slim
    shims: [python, pip]
  node:
    image: docker.io/library/node:22-slim
    shims: [node, npm]
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := LoadGlobalConfigAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Version != GlobalConfigVersion {
		t.Fatalf("version after migration: got %d, want %d", c.Version, GlobalConfigVersion)
	}
	for name, tool := range c.Tools {
		if !tool.PinnedGlobally {
			t.Fatalf("tool %q: PinnedGlobally should be true after v1→v2 migration", name)
		}
	}
}

func TestLoadGlobalConfigV2RespectsField(t *testing.T) {
	// On v2, the field is authoritative — a sync-installed tool stays
	// unpinned, a deliberately-installed tool stays pinned.
	dir := t.TempDir()
	path := dir + "/config.yaml"
	src := `version: 2
tools:
  python:
    image: docker.io/library/python:3.12-slim
    pinnedGlobally: true
    shims: [python]
  node:
    image: docker.io/library/node:22-slim
    shims: [node, npm]
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := LoadGlobalConfigAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Tools["python"].PinnedGlobally {
		t.Fatal("python should remain pinned on v2 load")
	}
	if c.Tools["node"].PinnedGlobally {
		t.Fatal("node should remain unpinned on v2 load")
	}
}

func TestResolveShimAll(t *testing.T) {
	src := `
version: 1
tools:
  python:
    image: docker.io/library/python:3.12-slim
    shims:
      - python
      - pip
  python2:
    image: docker.io/library/python:2.7-slim
    shims:
      - python
  node:
    image: docker.io/library/node:22-slim
    shims:
      - node
      - npm
`
	var c GlobalConfig
	if err := yaml.Unmarshal([]byte(src), &c); err != nil {
		t.Fatal(err)
	}

	if got := c.ResolveShimAll("missing"); got != nil {
		t.Fatalf("expected nil for unknown shim, got %v", got)
	}
	if got := c.ResolveShimAll("npm"); !reflect.DeepEqual(got, []string{"node"}) {
		t.Fatalf("unique match: got %v", got)
	}
	if got := c.ResolveShimAll("python"); !reflect.DeepEqual(got, []string{"python", "python2"}) {
		t.Fatalf("multi-match must be sorted: got %v", got)
	}
}
