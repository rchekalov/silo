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
