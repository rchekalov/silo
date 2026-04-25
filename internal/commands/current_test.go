// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rchekalov/silo/internal/config"
	"github.com/rchekalov/silo/internal/runtime"
	"github.com/rchekalov/silo/internal/tools"
)

func TestDispatchStatusFor(t *testing.T) {
	tests := []struct {
		name        string
		claimed     bool
		pinned      bool
		projectRoot string
		want        string
	}{
		// Project claim wins over the pin: even if the user ran `silo install`
		// (pinned), a .siloconf in this tree decides the version.
		{"project root claim wins over pin", true, true, "/tmp/proj", "claimed by /tmp/proj"},
		{"project root claim, unpinned", true, false, "/tmp/proj", "claimed by /tmp/proj"},
		// Claim from ~/.silo/siloconf surfaces with no project root.
		{"global siloconf claim", true, false, "", "claimed by global ~/.silo/siloconf"},
		// Pin without claim — silo handles it everywhere.
		{"globally pinned no claim", false, true, "", "globally pinned"},
		{"globally pinned no claim, projectRoot ignored", false, true, "/some/where", "globally pinned"},
		// Neither — shim falls through.
		{"unpinned no claim", false, false, "", "fall-through (no project claim, not pinned globally)"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := dispatchStatusFor(tc.claimed, tc.pinned, tc.projectRoot)
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCurrentMarker(t *testing.T) {
	pinned := true
	unpinned := false
	claiming := &config.ProjectConfig{Tools: []string{"node"}}
	notClaiming := &config.ProjectConfig{Tools: []string{"ruby"}}

	tests := []struct {
		name           string
		tool           string
		pinnedGlobally bool
		merged         *config.ProjectConfig
		want           string
	}{
		{"project claim beats pin", "node", pinned, claiming, "[project]"},
		{"project claim, unpinned", "node", unpinned, claiming, "[project]"},
		{"unpinned, unclaimed", "node", unpinned, notClaiming, "[fall-through]"},
		{"pinned, unclaimed", "node", pinned, notClaiming, "[pinned]"},
		{"nil merged + pinned", "node", pinned, nil, "[pinned]"},
		{"nil merged + unpinned", "node", unpinned, nil, "[fall-through]"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := currentMarker(tc.tool, tc.pinnedGlobally, tc.merged); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRenderCurrentSummary(t *testing.T) {
	// Three tools demonstrating the three markers; one with a project
	// override image to verify the (project: ...) suffix stays.
	g := &config.GlobalConfig{
		Version: 2,
		Tools: map[string]config.ToolDefinition{
			"node":   {Image: "node:22-slim", PinnedGlobally: false},
			"python": {Image: "python:3.12", PinnedGlobally: true},
			"ruby":   {Image: "ruby:3.3", PinnedGlobally: false},
		},
	}
	merged := &config.ProjectConfig{
		Tools: []string{"node"},
		Overrides: map[string]config.ToolOverride{
			"node": {Image: "node:18-slim"},
		},
	}
	var buf bytes.Buffer
	if err := renderCurrentSummary(&buf, g, merged); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	wantSub := []string{
		"Installed tools (3):",
		"node",
		"node:22-slim",
		"(project: node:18-slim)",
		"[project]",
		"python",
		"[pinned]",
		"ruby",
		"[fall-through]",
	}
	for _, s := range wantSub {
		if !strings.Contains(out, s) {
			t.Fatalf("output missing %q; got:\n%s", s, out)
		}
	}
}

func TestRenderCurrentSummaryEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := renderCurrentSummary(&buf, &config.GlobalConfig{}, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "No tools installed") {
		t.Fatalf("expected empty-state message; got %q", buf.String())
	}
}

func TestRenderInstalledList(t *testing.T) {
	// `silo list` table: PINNED column reflects the flag; SHIMS joined
	// with ", "; tools sorted alphabetically.
	g := &config.GlobalConfig{
		Version: 2,
		Tools: map[string]config.ToolDefinition{
			"python": {
				Image:          "python:3.12",
				PinnedGlobally: true,
				Shims: []config.ShimMapping{
					{HostCommand: "python", ContainerCommand: "python"},
					{HostCommand: "pip", ContainerCommand: "pip"},
				},
			},
			"node": {
				Image:          "node:22",
				PinnedGlobally: false,
				Shims: []config.ShimMapping{
					{HostCommand: "node", ContainerCommand: "node"},
				},
			},
		},
	}
	var buf bytes.Buffer
	if err := renderInstalled(&buf, g, nil, nil); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "TOOL") || !strings.Contains(out, "PINNED") {
		t.Fatalf("expected header with PINNED column; got:\n%s", out)
	}
	// The two rows must surface the right pinned values.
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var nodeLine, pythonLine string
	for _, ln := range lines {
		if strings.HasPrefix(strings.TrimSpace(ln), "node ") {
			nodeLine = ln
		}
		if strings.HasPrefix(strings.TrimSpace(ln), "python ") {
			pythonLine = ln
		}
	}
	if nodeLine == "" || pythonLine == "" {
		t.Fatalf("missing rows in output:\n%s", out)
	}
	if !strings.Contains(nodeLine, "no") {
		t.Fatalf("node row should show pinned=no; got %q", nodeLine)
	}
	if !strings.Contains(pythonLine, "yes") {
		t.Fatalf("python row should show pinned=yes; got %q", pythonLine)
	}
	// Sort order: node before python alphabetically.
	if strings.Index(out, "node") > strings.Index(out, "python") {
		t.Fatal("rows should be sorted alphabetically")
	}
}

func TestRenderInstalledListEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := renderInstalled(&buf, &config.GlobalConfig{}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "No tools installed") {
		t.Fatalf("expected empty-state message; got %q", buf.String())
	}
}

func TestRenderInstalledIncludesProjectPinnedImages(t *testing.T) {
	g := &config.GlobalConfig{
		Tools: map[string]config.ToolDefinition{
			"node": {
				Image:          "docker.io/library/node:22-slim",
				PinnedGlobally: true,
				Shims: []config.ShimMapping{
					{HostCommand: "node", ContainerCommand: "node"},
				},
			},
		},
	}
	imgState := map[string]runtime.ImageStateEntry{
		// Globally registered — should appear once via the cfg row, not
		// duplicated.
		"docker.io/library/node:22-slim": {Digest: "sha256:aaaa"},
		// Project-pinned only — should appear as an extra row with
		// pinned=project.
		"docker.io/library/node:18-slim": {Digest: "sha256:bbbb"},
	}
	registry := map[string]tools.RegistryEntry{
		"node": {
			Image: "docker.io/library/node:22-slim",
			Shims: []config.ShimMapping{
				{HostCommand: "node", ContainerCommand: "node"},
			},
		},
	}
	var buf bytes.Buffer
	if err := renderInstalled(&buf, g, imgState, registry); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "node:22-slim") || !strings.Contains(out, "yes") {
		t.Fatalf("expected globally-pinned node:22-slim row; got:\n%s", out)
	}
	if !strings.Contains(out, "node:18-slim") || !strings.Contains(out, "project") {
		t.Fatalf("expected project-pinned node:18-slim row; got:\n%s", out)
	}
	if c := strings.Count(out, "node:22-slim"); c != 1 {
		t.Fatalf("globally-pinned image should not be duplicated, got %d occurrences:\n%s", c, out)
	}
}

func TestDispatchStatusBridgesToFor(t *testing.T) {
	// Smoke test the wrapper: it should return the same string as
	// dispatchStatusFor for the canonical inputs. Guards against accidental
	// drift between the two when the rendering changes.
	def := config.ToolDefinition{Image: "x", PinnedGlobally: true}
	pc := &config.ProjectConfig{Tools: []string{"node"}}

	if got := dispatchStatus("node", def, pc, "/p"); got != "claimed by /p" {
		t.Fatalf("project claim path: got %q", got)
	}
	if got := dispatchStatus("ruby", def, pc, "/p"); got != "globally pinned" {
		t.Fatalf("pin path: got %q", got)
	}
	def.PinnedGlobally = false
	if got := dispatchStatus("ruby", def, pc, "/p"); got != "fall-through (no project claim, not pinned globally)" {
		t.Fatalf("fall-through path: got %q", got)
	}
}
