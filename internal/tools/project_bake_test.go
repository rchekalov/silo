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

// ApplyProjectFullBake distinguishes three outcomes via its return shape,
// which sync's call site in pull.go relies on to print the right message:
//   - (false, "",  nil) — empty steps; no bake recipe applied at all
//   - (true,  hash, nil) — fresh bake produced
//   - (false, hash, nil) — bake already exists at recipe hash (idempotent)

func TestApplyProjectFullBakeNoOpOnEmpty(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	called := false
	run := func(string, config.ToolDefinition, string, []string, string, bool) (int32, error) {
		called = true
		return 0, nil
	}
	baked, hash, err := ApplyProjectFullBake(run, "node", config.ToolDefinition{Image: "node:18-slim"}, nil, dir)
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

func TestApplyProjectFullBakeRunsFromScratch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	var seen struct {
		fromScratch bool
	}
	run := func(_ string, _ config.ToolDefinition, _ string, _ []string, target string, fromScratch bool) (int32, error) {
		seen.fromScratch = fromScratch
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return -1, err
		}
		return 0, os.WriteFile(target, []byte("mock-rootfs"), 0o644)
	}
	steps := []string{"apt-get install foo"}
	baked, hash, err := ApplyProjectFullBake(run, "node", config.ToolDefinition{Image: "node:18-slim"}, steps, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !baked || hash == "" {
		t.Fatalf("expected fresh bake, got baked=%v hash=%q", baked, hash)
	}
	if !seen.fromScratch {
		t.Fatal("full bake should pass FromScratch=true to the engine")
	}
}

func TestApplyProjectFullBakeIdempotent(t *testing.T) {
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
	steps := []string{"apt-get install foo"}
	def := config.ToolDefinition{Image: "node:18-slim"}

	first, hash1, err := ApplyProjectFullBake(run, "node", def, steps, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !first || hash1 == "" {
		t.Fatalf("first call should bake, got baked=%v hash=%q", first, hash1)
	}
	second, hash2, err := ApplyProjectFullBake(run, "node", def, steps, dir)
	if err != nil {
		t.Fatal(err)
	}
	if second {
		t.Fatal("second call should report no bake ran")
	}
	if hash2 != hash1 {
		t.Fatalf("recipe hash should be stable across calls: %q vs %q", hash2, hash1)
	}
	if runs != 1 {
		t.Fatalf("expected exactly one bake, got %d", runs)
	}
}

func TestApplyProjectFullBakeHashIncludesImage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	run := func(_ string, _ config.ToolDefinition, _ string, _ []string, target string, _ bool) (int32, error) {
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return -1, err
		}
		return 0, os.WriteFile(target, []byte("mock-rootfs"), 0o644)
	}
	steps := []string{"apt-get install foo"}

	_, hashA, err := ApplyProjectFullBake(run, "node", config.ToolDefinition{Image: "node:18-slim"}, steps, dir)
	if err != nil {
		t.Fatal(err)
	}
	_, hashB, err := ApplyProjectFullBake(run, "node", config.ToolDefinition{Image: "node:20-slim"}, steps, dir)
	if err != nil {
		t.Fatal(err)
	}
	if hashA == hashB {
		t.Fatal("recipe hash must differ when the pinned image changes")
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
