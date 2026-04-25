// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rchekalov/silo/internal/config"
)

func newTestCfg() *config.GlobalConfig {
	return &config.GlobalConfig{
		Version: 1,
		Tools: map[string]config.ToolDefinition{
			"node": {
				Image: "node:22-slim",
				Shims: []config.ShimMapping{
					{HostCommand: "node", ContainerCommand: "node"},
					{HostCommand: "npm", ContainerCommand: "npm"},
					{HostCommand: "npx", ContainerCommand: "npx"},
				},
			},
			"python": {
				Image: "python:3.12-slim",
				Shims: []config.ShimMapping{
					{HostCommand: "python", ContainerCommand: "python"},
					{HostCommand: "pip", ContainerCommand: "pip"},
				},
			},
		},
	}
}

func TestApplySiblingShim(t *testing.T) {
	tests := []struct {
		name           string
		tool           string
		command        string
		passthrough    []string
		wantCommand    string
		wantPass       []string
		wantStderrSub  string // substring expected in stderr; "" means stderr must be empty
	}{
		{
			name:        "same-tool shim is promoted",
			tool:        "node",
			command:     "node",
			passthrough: []string{"npm", "run", "dev"},
			wantCommand: "npm",
			wantPass:    []string{"run", "dev"},
		},
		{
			name:        "same-tool shim npx is promoted",
			tool:        "node",
			command:     "node",
			passthrough: []string{"npx", "tsc", "--version"},
			wantCommand: "npx",
			wantPass:    []string{"tsc", "--version"},
		},
		{
			name:          "cross-tool shim emits hint and does not rewrite",
			tool:          "node",
			command:       "node",
			passthrough:   []string{"python", "foo.py"},
			wantCommand:   "node",
			wantPass:      []string{"python", "foo.py"},
			wantStderrSub: `"python" is a shim of python, not node`,
		},
		{
			name:        "non-shim arg is left alone",
			tool:        "node",
			command:     "node",
			passthrough: []string{"script.js"},
			wantCommand: "node",
			wantPass:    []string{"script.js"},
		},
		{
			name:        "leading-dash arg is not promoted",
			tool:        "node",
			command:     "node",
			passthrough: []string{"--help"},
			wantCommand: "node",
			wantPass:    []string{"--help"},
		},
		{
			name:        "empty passthrough is a no-op",
			tool:        "node",
			command:     "node",
			passthrough: nil,
			wantCommand: "node",
			wantPass:    nil,
		},
		{
			name:        "arg equal to command is not double-promoted",
			tool:        "node",
			command:     "node",
			passthrough: []string{"node", "script.js"},
			wantCommand: "node",
			wantPass:    []string{"node", "script.js"},
		},
	}

	cfg := newTestCfg()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			gotCmd, gotPass := applySiblingShim(cfg, tt.tool, tt.command, tt.passthrough, &buf)
			if gotCmd != tt.wantCommand {
				t.Errorf("command: got %q, want %q", gotCmd, tt.wantCommand)
			}
			if !equalSlices(gotPass, tt.wantPass) {
				t.Errorf("passthrough: got %v, want %v", gotPass, tt.wantPass)
			}
			stderr := buf.String()
			if tt.wantStderrSub == "" {
				if stderr != "" {
					t.Errorf("expected empty stderr, got %q", stderr)
				}
			} else if !strings.Contains(stderr, tt.wantStderrSub) {
				t.Errorf("stderr missing %q; got %q", tt.wantStderrSub, stderr)
			}
		})
	}
}

func TestResolveFallThroughStripsShimBinAndDispatchMarker(t *testing.T) {
	// Lay out PATH as: <silo bin>:<real bin>:<unrelated>. Drop a fake
	// "tool" executable in the real bin only. resolveFallThrough must
	// strip the silo bin, find the real one, and return an env that has
	// the filtered PATH plus drops _SILO_SHIM_DISPATCH.
	shimBin := t.TempDir()
	realBin := t.TempDir()
	unrelated := t.TempDir()

	exe := filepath.Join(realBin, "tool")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	inboundPATH := strings.Join([]string{shimBin, realBin, unrelated}, string(filepath.ListSeparator))
	inboundEnv := []string{
		"KEEP=1",
		"PATH=" + inboundPATH,
		"_SILO_SHIM_DISPATCH=1",
	}

	// resolveFallThrough mutates process PATH transiently for the LookPath
	// call. Anchor the test's PATH so failures don't leak across tests.
	t.Setenv("PATH", inboundPATH)

	next, env, err := resolveFallThrough("tool", shimBin, inboundPATH, inboundEnv)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if next != exe {
		t.Fatalf("next: got %q, want %q", next, exe)
	}

	wantPATH := "PATH=" + strings.Join([]string{realBin, unrelated}, string(filepath.ListSeparator))
	if !slices.Contains(env, wantPATH) {
		t.Fatalf("env missing filtered PATH %q; got %v", wantPATH, env)
	}
	if !slices.Contains(env, "KEEP=1") {
		t.Fatalf("env should preserve unrelated keys; got %v", env)
	}
	for _, kv := range env {
		if strings.HasPrefix(kv, "_SILO_SHIM_DISPATCH=") {
			t.Fatalf("env should drop _SILO_SHIM_DISPATCH; got %v", env)
		}
		if strings.HasPrefix(kv, "PATH=") && kv != wantPATH {
			t.Fatalf("env should not retain inbound PATH; got %q (want only %q)", kv, wantPATH)
		}
	}
}

func TestResolveFallThroughNotFoundError(t *testing.T) {
	// PATH contains only the silo bin; after stripping, nothing is left
	// to look up. The returned error must mention the command, the silo
	// bin path that was stripped, and both remediation hints.
	shimBin := t.TempDir()
	t.Setenv("PATH", shimBin)

	_, _, err := resolveFallThrough("definitely-not-a-real-command-zzzz", shimBin, shimBin, []string{"PATH=" + shimBin})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	msg := err.Error()
	for _, fragment := range []string{
		"definitely-not-a-real-command-zzzz",
		shimBin,
		"silo install",
		"silo use",
	} {
		if !strings.Contains(msg, fragment) {
			t.Fatalf("error missing %q: %q", fragment, msg)
		}
	}
}

func TestResolveFallThroughDuplicateShimBinEntries(t *testing.T) {
	// Some shells happily prepend the same dir twice. Stripping must drop
	// every occurrence, otherwise a fall-through would loop back into the
	// silo shim it's trying to escape.
	shimBin := t.TempDir()
	realBin := t.TempDir()
	exe := filepath.Join(realBin, "dup")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	inboundPATH := strings.Join([]string{shimBin, shimBin, realBin}, string(filepath.ListSeparator))
	t.Setenv("PATH", inboundPATH)

	_, env, err := resolveFallThrough("dup", shimBin, inboundPATH, []string{"PATH=" + inboundPATH})
	if err != nil {
		t.Fatal(err)
	}
	wantPATH := "PATH=" + realBin
	if !slices.Contains(env, wantPATH) {
		t.Fatalf("filtered PATH should drop ALL silo bin entries; got %v", env)
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
