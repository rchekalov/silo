// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"strings"
	"testing"
)

func TestParseSpec(t *testing.T) {
	cases := []struct {
		in, name, version, wantErr string
	}{
		{"python", "python", "", ""},
		{"python@3.12", "python", "3.12", ""},
		{"node@20", "node", "20", ""},
		{"", "", "", "empty tool spec"},
		{"python@", "", "", "empty version"},
		{"@3.12", "", "", "missing name"},
	}
	for _, c := range cases {
		name, version, err := ParseSpec(c.in)
		if c.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("ParseSpec(%q): want error containing %q, got %v", c.in, c.wantErr, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseSpec(%q): unexpected error %v", c.in, err)
			continue
		}
		if name != c.name || version != c.version {
			t.Errorf("ParseSpec(%q): got (%q,%q), want (%q,%q)", c.in, name, version, c.name, c.version)
		}
	}
}

func TestLoadBuiltinRegistry(t *testing.T) {
	entries, err := Entries()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"python", "node", "rust", "go", "deno"} {
		if _, ok := entries[name]; !ok {
			t.Fatalf("missing %q", name)
		}
	}
}

func TestLookupPython(t *testing.T) {
	def, ok, err := Lookup("python", "")
	if err != nil || !ok {
		t.Fatalf("err=%v ok=%v", err, ok)
	}
	if def.Image != "docker.io/library/python:3.14-slim" {
		t.Fatalf("image %q", def.Image)
	}
	hasPip := false
	for _, s := range def.Shims {
		if s.HostCommand == "pip" {
			hasPip = true
		}
	}
	if !hasPip {
		t.Fatal("no pip shim")
	}
}

func TestLookupWithVersion(t *testing.T) {
	def, ok, err := Lookup("python", "3.11-slim")
	if err != nil || !ok {
		t.Fatalf("err=%v ok=%v", err, ok)
	}
	if def.Image != "docker.io/library/python:3.11-slim" {
		t.Fatalf("image %q", def.Image)
	}
}

func TestLookupUnknownVersionRejected(t *testing.T) {
	// Previously this accepted any string and silently swapped the tag,
	// so `python@3.13` landed on `python:3.13` (not -slim) — ~900 MB
	// vs ~50 MB slim. Now we reject unknown versions up front and tell
	// the user what's available.
	_, _, err := Lookup("python", "3.13")
	if err == nil {
		t.Fatal("expected error for unknown python version, got nil")
	}
	if !strings.Contains(err.Error(), "unknown version") {
		t.Fatalf("error should mention unknown version, got %v", err)
	}
	if !strings.Contains(err.Error(), "3.14-slim") {
		t.Fatalf("error should list available versions including 3.14-slim, got %v", err)
	}
	if !strings.Contains(err.Error(), "--image") {
		t.Fatalf("error should point at --image escape hatch, got %v", err)
	}
}

func TestDefaultVersion(t *testing.T) {
	v, err := DefaultVersion("python")
	if err != nil {
		t.Fatal(err)
	}
	if v != "3.14-slim" {
		t.Fatalf("version %q", v)
	}
}

func TestAvailableTools(t *testing.T) {
	names, err := AvailableTools()
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, n := range names {
		found[n] = true
	}
	if !found["python"] || !found["node"] {
		t.Fatalf("expected python + node in %+v", names)
	}
}

func TestEntryToToolDefinitionDefaults(t *testing.T) {
	e, ok, err := LookupEntry("deno")
	if err != nil || !ok {
		t.Fatalf("err=%v ok=%v", err, ok)
	}
	def := e.ToToolDefinition("")
	if def.CPUs != 2 || def.MemoryMB != 2048 || def.Workdir != "/workspace" {
		t.Fatalf("defaults: %+v", def)
	}
}

func TestEntryCustomResources(t *testing.T) {
	e, ok, err := LookupEntry("playwright")
	if err != nil || !ok {
		t.Fatalf("err=%v ok=%v", err, ok)
	}
	def := e.ToToolDefinition("")
	if def.CPUs != 4 || def.MemoryMB != 4096 || def.RootfsSizeMB != 4096 {
		t.Fatalf("resources: %+v", def)
	}
	if def.Network == nil || len(def.Requires) == 0 {
		t.Fatalf("network/requires: %+v", def)
	}
}
