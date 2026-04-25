// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"bytes"
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
