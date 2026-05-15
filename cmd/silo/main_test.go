// SPDX-License-Identifier: Apache-2.0

package main

import (
	"reflect"
	"testing"

	"github.com/rchekalov/silo/internal/config"
)

// TestTransformArgsNewVerbs makes sure the 0.5.0 top-level verbs are NOT
// rewritten to `silo run <verb>` by the tool-shorthand transform. The
// reservedNames map in main.go is what enforces this; this test exists to
// keep a future edit to that map from silently regressing UX.
func TestTransformArgsNewVerbs(t *testing.T) {
	cfg := &config.GlobalConfig{
		Tools: map[string]config.ToolDefinition{
			// A realistic installed tool so the transform is actually running.
			"python": {Image: "docker.io/library/python:3.12-slim"},
		},
	}
	for _, verb := range []string{"use", "unuse", "sync", "apply", "build", "doctor", "current", "prune"} {
		t.Run(verb, func(t *testing.T) {
			args := []string{verb}
			pass := transformArgs(&args, cfg)
			if len(pass) != 0 {
				t.Errorf("%s: unexpected passthrough %+v", verb, pass)
			}
			if !reflect.DeepEqual(args, []string{verb}) {
				t.Errorf("%s: args rewritten to %+v, want [%q]", verb, args, verb)
			}
		})
	}
}

// TestTransformArgsToolShorthandStillWorks verifies the positive case: a bare
// installed tool name still gets rewritten to `silo run <tool>`.
func TestTransformArgsToolShorthandStillWorks(t *testing.T) {
	cfg := &config.GlobalConfig{
		Tools: map[string]config.ToolDefinition{
			"python": {Image: "docker.io/library/python:3.12-slim"},
		},
	}
	args := []string{"python", "-c", "print(1)"}
	pass := transformArgs(&args, cfg)
	wantArgs := []string{"run", "python"}
	wantPass := []string{"-c", "print(1)"}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Errorf("args=%+v, want %+v", args, wantArgs)
	}
	if !reflect.DeepEqual(pass, wantPass) {
		t.Errorf("passthrough=%+v, want %+v", pass, wantPass)
	}
}

// TestTransformArgsPositionalSplit covers the Docker-style split for `run`
// and `build`: silo flags before the tool, everything after the tool is
// pass-through. Known silo flags appearing after the tool are hoisted in
// front so the legacy post-tool form still works.
func TestTransformArgsPositionalSplit(t *testing.T) {
	cfg := &config.GlobalConfig{
		Tools: map[string]config.ToolDefinition{
			"python": {Image: "docker.io/library/python:3.12-slim"},
			"node":   {Image: "docker.io/library/node:20-slim"},
		},
	}

	tests := []struct {
		name     string
		argv     []string
		wantArgs []string
		wantPass []string
	}{
		{
			name:     "run no extra args",
			argv:     []string{"run", "python"},
			wantArgs: []string{"run", "python"},
			wantPass: nil,
		},
		{
			name:     "run with positional args",
			argv:     []string{"run", "python", "-c", "print(1)"},
			wantArgs: []string{"run", "python"},
			wantPass: []string{"-c", "print(1)"},
		},
		{
			name:     "run silo bool flag before tool",
			argv:     []string{"run", "--timing", "python", "-V"},
			wantArgs: []string{"run", "--timing", "python"},
			wantPass: []string{"-V"},
		},
		{
			name:     "run silo value flag before tool",
			argv:     []string{"run", "--shim", "pip", "python", "install", "foo"},
			wantArgs: []string{"run", "--shim", "pip", "python"},
			wantPass: []string{"install", "foo"},
		},
		{
			name:     "run --flag=value form",
			argv:     []string{"run", "--shim=pip", "python", "install", "foo"},
			wantArgs: []string{"run", "--shim=pip", "python"},
			wantPass: []string{"install", "foo"},
		},
		{
			name:     "build with command",
			argv:     []string{"build", "node", "npm", "install"},
			wantArgs: []string{"build", "node"},
			wantPass: []string{"npm", "install"},
		},
		{
			name:     "build silo flag before tool",
			argv:     []string{"build", "--rerun", "node"},
			wantArgs: []string{"build", "--rerun", "node"},
			wantPass: nil,
		},
		{
			name:     "build silo flag after tool is hoisted",
			argv:     []string{"build", "node", "--remove"},
			wantArgs: []string{"build", "--remove", "node"},
			wantPass: nil,
		},
		{
			name:     "build --all --rerun no tool",
			argv:     []string{"build", "--all", "--rerun"},
			wantArgs: []string{"build", "--all", "--rerun"},
			wantPass: nil,
		},
		{
			name:     "build global flag plus command",
			argv:     []string{"build", "--global", "node", "npm", "install"},
			wantArgs: []string{"build", "--global", "node"},
			wantPass: []string{"npm", "install"},
		},
		{
			name:     "legacy -- still works",
			argv:     []string{"run", "python", "--", "-c", "print(1)"},
			wantArgs: []string{"run", "python"},
			wantPass: []string{"-c", "print(1)"},
		},
		{
			name:     "legacy -- with timing flag",
			argv:     []string{"run", "python", "--timing", "--", "--version"},
			wantArgs: []string{"run", "python", "--timing"},
			wantPass: []string{"--version"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string(nil), tc.argv...)
			pass := transformArgs(&args, cfg)
			if !reflect.DeepEqual(args, tc.wantArgs) {
				t.Errorf("args=%+v, want %+v", args, tc.wantArgs)
			}
			if !reflect.DeepEqual(pass, tc.wantPass) {
				t.Errorf("passthrough=%+v, want %+v", pass, tc.wantPass)
			}
		})
	}
}

// TestTransformArgsToolShorthandWithFlags covers the wrap path: a bare tool
// name with positional args after it (no `--` inserted), then the positional
// split treats them as pass-through.
func TestTransformArgsToolShorthandWithFlags(t *testing.T) {
	cfg := &config.GlobalConfig{
		Tools: map[string]config.ToolDefinition{
			"python": {Image: "docker.io/library/python:3.12-slim"},
		},
	}
	// `silo python -c "print(1)"` → `run python` + passthrough.
	args := []string{"python", "-c", "print(1)"}
	pass := transformArgs(&args, cfg)
	if !reflect.DeepEqual(args, []string{"run", "python"}) {
		t.Errorf("args=%+v", args)
	}
	if !reflect.DeepEqual(pass, []string{"-c", "print(1)"}) {
		t.Errorf("passthrough=%+v", pass)
	}
}

// TestTransformArgsShimWrap verifies that resolving a shim shorthand
// (`silo npm test` when npm is a node shim) wraps it with --shim and the
// remaining args end up as pass-through, with no `--` inserted.
func TestTransformArgsShimWrap(t *testing.T) {
	cfg := &config.GlobalConfig{
		Tools: map[string]config.ToolDefinition{
			"node": {
				Image: "docker.io/library/node:20-slim",
				Shims: []config.ShimMapping{
					{HostCommand: "node", ContainerCommand: "node"},
					{HostCommand: "npm", ContainerCommand: "npm"},
				},
			},
		},
	}
	args := []string{"npm", "test"}
	pass := transformArgs(&args, cfg)
	// transformArgs first wraps to `run node --shim npm test`, then the
	// positional split hoists `--shim npm` in front of the tool name.
	wantArgs := []string{"run", "--shim", "npm", "node"}
	wantPass := []string{"test"}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Errorf("args=%+v, want %+v", args, wantArgs)
	}
	if !reflect.DeepEqual(pass, wantPass) {
		t.Errorf("passthrough=%+v, want %+v", pass, wantPass)
	}
}

// TestTransformArgsShimDoesntFireOnReserved makes sure reserved subcommands
// (like `build`, `sync`, `doctor`) aren't mis-resolved as tool/shim names
// even when they happen to collide with installed tools.
func TestTransformArgsShimDoesntFireOnReserved(t *testing.T) {
	cfg := &config.GlobalConfig{
		Tools: map[string]config.ToolDefinition{
			"python": {Image: "docker.io/library/python:3.12-slim"},
		},
	}
	args := []string{"shim", "python", "add"}
	pass := transformArgs(&args, cfg)
	// `silo shim <tool> <action>` is rearranged to `silo shim <action> <tool>`.
	wantArgs := []string{"shim", "add", "python"}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Errorf("args=%+v, want %+v", args, wantArgs)
	}
	if pass != nil {
		t.Errorf("unexpected passthrough %+v", pass)
	}
}
