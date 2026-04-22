// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectNode(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "package.json"))
	got := Detect(dir)
	if len(got) != 1 || got[0].Name != "node" {
		t.Fatalf("got %+v", got)
	}
}

func TestDetectMultiple(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "package.json"))
	touch(t, filepath.Join(dir, "requirements.txt"))
	touch(t, filepath.Join(dir, "pyproject.toml"))
	got := Detect(dir)
	// markerMap order: python, node, rust, go, deno.
	if len(got) != 2 || got[0].Name != "python" || got[1].Name != "node" {
		t.Fatalf("got %+v", got)
	}
	if len(got[0].Markers) != 2 {
		t.Fatalf("python markers: %+v", got[0].Markers)
	}
}

func TestDetectNone(t *testing.T) {
	if got := Detect(t.TempDir()); got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

func TestCollectExcludes(t *testing.T) {
	got := CollectExcludes([]string{"node", "python"})
	want := map[string]bool{"node_modules": true, ".venv": true, "__pycache__": true}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for _, e := range got {
		if !want[e] {
			t.Fatalf("unexpected exclude %q", e)
		}
	}
}

func TestCollectExcludesDedup(t *testing.T) {
	got := CollectExcludes([]string{"node", "node"})
	if len(got) != 1 || got[0] != "node_modules" {
		t.Fatalf("got %+v", got)
	}
}

func TestDetectAddonsKotlin(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "build.gradle.kts"))
	got := DetectAddons(dir)
	if len(got) != 1 || got[0].Name != "kotlin" {
		t.Fatalf("got %+v", got)
	}
}

func TestDetectAddonsJavaPom(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "pom.xml"))
	got := DetectAddons(dir)
	if len(got) != 1 || got[0].Name != "java" {
		t.Fatalf("got %+v", got)
	}
}

func TestDetectAddonsDoesNotMatchFirstClass(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "package.json"))
	touch(t, filepath.Join(dir, "requirements.txt"))
	if got := DetectAddons(dir); len(got) != 0 {
		t.Fatalf("expected no addons, got %+v", got)
	}
}

func TestDetectAddonsNone(t *testing.T) {
	if got := DetectAddons(t.TempDir()); got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}
