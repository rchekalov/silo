// SPDX-License-Identifier: Apache-2.0

package config

import (
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
