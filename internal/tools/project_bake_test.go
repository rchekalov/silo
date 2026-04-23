// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rchekalov/silo/internal/config"
)

// ApplyProjectPostInstall uses runtime.ProjectRootfs to compute the target
// path, which reads from <projectRoot>/.silo/<tool>/rootfs.ext4. The hash
// sidecar is written next to it on success.

func TestApplyProjectPostInstallNoOpOnEmpty(t *testing.T) {
	dir := t.TempDir()
	called := false
	run := func(string, config.ToolDefinition, string, []string, string, bool) (int32, error) {
		called = true
		return 0, nil
	}
	baked, err := ApplyProjectPostInstall(run, "claude-code", config.ToolDefinition{}, nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	if baked {
		t.Fatal("empty steps should not bake")
	}
	if called {
		t.Fatal("BakeFunc should not be called for empty steps")
	}
}

func TestApplyProjectPostInstallRequiresProjectRoot(t *testing.T) {
	run := func(string, config.ToolDefinition, string, []string, string, bool) (int32, error) {
		return 0, nil
	}
	_, err := ApplyProjectPostInstall(run, "claude-code", config.ToolDefinition{}, []string{"step"}, "")
	if err == nil {
		t.Fatal("expected error without project root")
	}
}

func TestApplyProjectPostInstallRunsAndPersistsHash(t *testing.T) {
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
	baked, err := ApplyProjectPostInstall(run, "claude-code", config.ToolDefinition{Image: "node:22-slim"}, steps, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !baked {
		t.Fatal("first bake should return baked=true")
	}
	if seen.global {
		t.Fatal("project bake should invoke run with global=false")
	}
	wantTarget := filepath.Join(dir, ".silo", "claude-code", "rootfs.ext4")
	if seen.target != wantTarget {
		t.Fatalf("target=%q want %q", seen.target, wantTarget)
	}
	// Hash sidecar exists.
	if _, err := os.Stat(wantTarget + ".sha256"); err != nil {
		t.Fatalf("hash sidecar missing: %v", err)
	}
}

func TestApplyProjectPostInstallIdempotent(t *testing.T) {
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
		if _, err := ApplyProjectPostInstall(run, "claude-code", config.ToolDefinition{}, steps, dir); err != nil {
			t.Fatal(err)
		}
	}
	if runs != 1 {
		t.Fatalf("expected exactly one bake, got %d", runs)
	}
}

func TestApplyProjectPostInstallRebakesOnStepChange(t *testing.T) {
	dir := t.TempDir()
	runs := 0
	run := func(_ string, _ config.ToolDefinition, _ string, _ []string, target string, _ bool) (int32, error) {
		runs++
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return -1, err
		}
		return 0, os.WriteFile(target, []byte("mock-rootfs"), 0o644)
	}

	if _, err := ApplyProjectPostInstall(run, "claude-code", config.ToolDefinition{}, []string{"step-a"}, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyProjectPostInstall(run, "claude-code", config.ToolDefinition{}, []string{"step-a", "step-b"}, dir); err != nil {
		t.Fatal(err)
	}
	if runs != 2 {
		t.Fatalf("expected re-bake on step change, got %d runs", runs)
	}
}
