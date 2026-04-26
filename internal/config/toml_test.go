// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

// TestProjectConfigYAMLToTOMLRoundTrip parses a representative YAML config,
// re-emits it as TOML, parses the TOML back, and asserts struct equality.
// This is the contract that makes `silo config migrate` safe — any field
// that fails this check would lose user data on migration.
func TestProjectConfigYAMLToTOMLRoundTrip(t *testing.T) {
	yamlSrc := []byte(`
tools: [python, node]
passEnv:
  - GITHUB_TOKEN
passFiles:
  - .npmrc
passSshAgent: true
project_id: my-project-42
overrides:
  python:
    image: docker.io/library/python:3.11-slim
    workdir: /app
    cpus: 4
    memoryMB: 6144
    rootfsSizeMB: 4096
    env:
      PYTHONPATH: /app/src
    network:
      hostAccess: true
      proxy:
        allow: [pypi.org, "*.pythonhosted.org"]
    ports:
      - host: 8000
        guest: 8000
    passEnv: [GITHUB_TOKEN]
    passSshAgent: true
    postInstall:
      - apt-get install -y git
    cache:
      - guest: /root/.cache/pip
        host: ~/.silo/cache/python/pip
    lsp:
      command: [pyright-langserver, --stdio]
      install: npm i -g pyright@1.1.350
      env:
        PYRIGHT_LOG: verbose
      cache:
        - guest: /root/.cache/pyright
          host: ~/.silo/cache/python/pyright
`)

	var fromYAML ProjectConfig
	if err := yaml.Unmarshal(yamlSrc, &fromYAML); err != nil {
		t.Fatalf("yaml unmarshal: %v", err)
	}

	tomlOut, err := toml.Marshal(&fromYAML)
	if err != nil {
		t.Fatalf("toml marshal: %v", err)
	}

	var fromTOML ProjectConfig
	if err := toml.Unmarshal(tomlOut, &fromTOML); err != nil {
		t.Fatalf("toml unmarshal: %v\nemitted toml:\n%s", err, tomlOut)
	}

	if !reflect.DeepEqual(fromYAML, fromTOML) {
		t.Fatalf("round-trip mismatch.\nyaml→struct: %#v\ntoml→struct: %#v\nemitted toml:\n%s",
			fromYAML, fromTOML, tomlOut)
	}
}

// TestLoadProjectConfigFileSniffsTOML proves silo.toml is parsed as TOML and
// .siloconf is parsed as YAML even though both go through LoadProjectConfigFile.
func TestLoadProjectConfigFileSniffsTOML(t *testing.T) {
	dir := t.TempDir()
	tomlPath := filepath.Join(dir, "silo.toml")
	yamlPath := filepath.Join(dir, ".siloconf")

	if err := os.WriteFile(tomlPath, []byte(`tools = ["python"]
passSshAgent = true
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(yamlPath, []byte(`tools: [node]
passSshAgent: false
`), 0o644); err != nil {
		t.Fatal(err)
	}

	tomlCfg, err := LoadProjectConfigFile(tomlPath)
	if err != nil {
		t.Fatalf("toml load: %v", err)
	}
	if len(tomlCfg.Tools) != 1 || tomlCfg.Tools[0] != "python" || !tomlCfg.PassSshAgent {
		t.Fatalf("toml unexpected: %+v", tomlCfg)
	}
	yamlCfg, err := LoadProjectConfigFile(yamlPath)
	if err != nil {
		t.Fatalf("yaml load: %v", err)
	}
	if len(yamlCfg.Tools) != 1 || yamlCfg.Tools[0] != "node" || yamlCfg.PassSshAgent {
		t.Fatalf("yaml unexpected: %+v", yamlCfg)
	}
}

// TestFindProjectConfigPrefersTOML asserts the walk-up picks silo.toml when
// both files coexist at the same level — important during the deprecation
// window where users may have both files lying around mid-migration.
func TestFindProjectConfigPrefersTOML(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "silo.toml"),
		[]byte(`tools = ["picked-from-toml"]`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".siloconf"),
		[]byte("tools: [picked-from-yaml]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, root, err := FindProjectConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if root != dir {
		t.Fatalf("root %q want %q", root, dir)
	}
	if len(cfg.Tools) != 1 || cfg.Tools[0] != "picked-from-toml" {
		t.Fatalf("expected silo.toml to win, got %+v", cfg)
	}
}

// TestSaveTOMLAndReload proves a written silo.toml round-trips through Load.
func TestSaveTOMLAndReload(t *testing.T) {
	dir := t.TempDir()
	original := ProjectConfig{
		Tools:        []string{"node", "python"},
		PassEnv:      []string{"GITHUB_TOKEN"},
		PassSshAgent: true,
		ProjectID:    "fixture",
		Overrides: map[string]ToolOverride{
			"node": {
				CPUs:         4,
				MemoryMB:     6144,
				PassSshAgent: true,
			},
		},
	}
	if err := original.SaveTOML(dir); err != nil {
		t.Fatalf("SaveTOML: %v", err)
	}
	loaded, err := LoadProjectConfigFile(filepath.Join(dir, ProjectConfigFilenameTOML))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(*loaded, original) {
		t.Fatalf("round-trip mismatch.\nwrote: %#v\nread:  %#v", original, *loaded)
	}
}

// TestShimMappingTOMLRoundTrip pins the encoding.TextMarshaler hook —
// shim mappings must serialize as plain TOML strings ("npm" or "npm2:npm"),
// matching the YAML scalar shape so the registry/global config files
// migrate cleanly.
func TestShimMappingTOMLRoundTrip(t *testing.T) {
	cases := []ShimMapping{
		{HostCommand: "npm", ContainerCommand: "npm"},
		{HostCommand: "npm2", ContainerCommand: "npm"},
	}
	for _, in := range cases {
		out, err := toml.Marshal(map[string]ShimMapping{"x": in})
		if err != nil {
			t.Fatalf("marshal %+v: %v", in, err)
		}
		var got map[string]ShimMapping
		if err := toml.Unmarshal(out, &got); err != nil {
			t.Fatalf("unmarshal %s: %v", out, err)
		}
		if got["x"] != in {
			t.Fatalf("round-trip lost data: in=%+v out=%+v emitted=%s", in, got["x"], out)
		}
	}
}
