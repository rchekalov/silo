// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rchekalov/silo/internal/config"
	"github.com/rchekalov/silo/internal/runtime"
)

// ApplyProjectPostInstall writes its rootfs to ~/.silo/baked/<recipe-hash>/
// (content-addressed). Tests t.Setenv("HOME", ...) so the path resolves
// under a temp dir and survives concurrent test runs.

func TestApplyProjectPostInstallNoOpOnEmpty(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	called := false
	run := func(string, config.ToolDefinition, string, []string, string, bool) (int32, error) {
		called = true
		return 0, nil
	}
	baked, hash, err := ApplyProjectPostInstall(run, "claude-code", config.ToolDefinition{}, nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	if baked {
		t.Fatal("empty steps should not bake")
	}
	if hash != "" {
		t.Fatalf("empty steps should produce no recipe hash, got %q", hash)
	}
	if called {
		t.Fatal("BakeFunc should not be called for empty steps")
	}
}

func TestApplyProjectPostInstallRequiresProjectRoot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	run := func(string, config.ToolDefinition, string, []string, string, bool) (int32, error) {
		return 0, nil
	}
	_, _, err := ApplyProjectPostInstall(run, "claude-code", config.ToolDefinition{}, []string{"step"}, "")
	if err == nil {
		t.Fatal("expected error without project root")
	}
}

func TestApplyProjectPostInstallRunsAndWritesManifest(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	var seen struct {
		target string
		global bool
		cmd    []string
	}
	run := func(_ string, _ config.ToolDefinition, cmd string, args []string, target string, global bool) (int32, error) {
		seen.target = target
		seen.global = global
		seen.cmd = append([]string{cmd}, args...)
		// Simulate the engine producing a rootfs.
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return -1, err
		}
		return 0, os.WriteFile(target, []byte("mock-rootfs"), 0o644)
	}

	steps := []string{"apt-get install kotlin"}
	baked, hash, err := ApplyProjectPostInstall(run, "claude-code", config.ToolDefinition{Image: "node:22-slim"}, steps, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !baked {
		t.Fatal("first bake should return baked=true")
	}
	if hash == "" {
		t.Fatal("baked call should return a non-empty recipe hash")
	}
	if seen.global {
		t.Fatal("project bake should invoke run with global=false")
	}
	wantTarget := runtime.BakedRootfs(hash)
	if seen.target != wantTarget {
		t.Fatalf("target=%q want %q", seen.target, wantTarget)
	}
	if _, err := os.Stat(runtime.BakedManifest(hash)); err != nil {
		t.Fatalf("manifest missing: %v", err)
	}
}

func TestApplyProjectPostInstallIdempotent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	runs := 0
	run := func(_ string, _ config.ToolDefinition, _ string, _ []string, target string, _ bool) (int32, error) {
		runs++
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return -1, err
		}
		return 0, os.WriteFile(target, []byte("mock-rootfs"), 0o644)
	}
	steps := []string{"apt-get install kotlin"}

	for i := 0; i < 3; i++ {
		if _, _, err := ApplyProjectPostInstall(run, "claude-code", config.ToolDefinition{}, steps, dir); err != nil {
			t.Fatal(err)
		}
	}
	if runs != 1 {
		t.Fatalf("expected exactly one bake, got %d", runs)
	}
}

func TestApplyProjectPostInstallRebakesOnStepChange(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	runs := 0
	run := func(_ string, _ config.ToolDefinition, _ string, _ []string, target string, _ bool) (int32, error) {
		runs++
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return -1, err
		}
		return 0, os.WriteFile(target, []byte("mock-rootfs"), 0o644)
	}

	if _, _, err := ApplyProjectPostInstall(run, "claude-code", config.ToolDefinition{}, []string{"step-a"}, dir); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ApplyProjectPostInstall(run, "claude-code", config.ToolDefinition{}, []string{"step-a", "step-b"}, dir); err != nil {
		t.Fatal(err)
	}
	if runs != 2 {
		t.Fatalf("expected re-bake on step change, got %d runs", runs)
	}
}
