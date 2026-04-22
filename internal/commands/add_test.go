// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"strings"
	"testing"

	"github.com/rchekalov/silo/internal/config"
)

func TestStepsFromAddArgsAptOnly(t *testing.T) {
	steps, summary, err := stepsFromAddArgs([]string{"ripgrep", "jq"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 {
		t.Fatalf("want 1 step for apt-only args, got %+v", steps)
	}
	// Packages should be sorted for stable hashing.
	if !strings.Contains(steps[0], "jq ripgrep") {
		t.Fatalf("unsorted or malformed step: %q", steps[0])
	}
	if len(summary) != 1 || !strings.Contains(summary[0], "apt") {
		t.Fatalf("summary %+v", summary)
	}
}

func TestStepsFromAddArgsExpandsLanguage(t *testing.T) {
	steps, summary, err := stepsFromAddArgs([]string{"kotlin"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) == 0 {
		t.Fatalf("kotlin language addon produced no steps")
	}
	joined := strings.Join(steps, "\n")
	if !strings.Contains(joined, "kotlin") || !strings.Contains(joined, "openjdk") {
		t.Fatalf("language expansion missing deps: %q", joined)
	}
	if len(summary) != 1 || !strings.Contains(summary[0], "language kotlin") {
		t.Fatalf("summary %+v", summary)
	}
}

func TestStepsFromAddArgsLanguagePlusApt(t *testing.T) {
	kotlinSteps, _, _ := stepsFromAddArgs([]string{"kotlin"}, "")
	mixed, _, err := stepsFromAddArgs([]string{"kotlin", "ripgrep"}, "")
	if err != nil {
		t.Fatal(err)
	}
	// Should be kotlin's steps + one apt step for ripgrep.
	if len(mixed) != len(kotlinSteps)+1 {
		t.Fatalf("want kotlin+1 apt step, got %d", len(mixed))
	}
}

func TestStepsFromAddArgsExplicitStep(t *testing.T) {
	steps, summary, err := stepsFromAddArgs(nil, "npm install -g typescript")
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0] != "npm install -g typescript" {
		t.Fatalf("unexpected: %+v", steps)
	}
	if !strings.Contains(summary[0], "step:") {
		t.Fatalf("summary %+v", summary)
	}
}

func TestStepsFromAddArgsEmpty(t *testing.T) {
	if _, _, err := stepsFromAddArgs(nil, ""); err == nil {
		t.Fatal("expected error for no args + no step")
	}
}

func TestAppendPostInstallStepsIdempotent(t *testing.T) {
	cfg := &config.ProjectConfig{}
	if err := appendPostInstallSteps(cfg, "claude-code", []string{"apt-get install kotlin"}); err != nil {
		t.Fatal(err)
	}
	if err := appendPostInstallSteps(cfg, "claude-code", []string{"apt-get install kotlin"}); err != nil {
		t.Fatal(err)
	}
	o := cfg.Overrides["claude-code"]
	if len(o.PostInstall) != 1 {
		t.Fatalf("duplicate step added: %+v", o.PostInstall)
	}
}

func TestAppendPostInstallStepsPreservesOrder(t *testing.T) {
	cfg := &config.ProjectConfig{}
	if err := appendPostInstallSteps(cfg, "claude-code", []string{"step-1"}); err != nil {
		t.Fatal(err)
	}
	if err := appendPostInstallSteps(cfg, "claude-code", []string{"step-2", "step-1", "step-3"}); err != nil {
		t.Fatal(err)
	}
	o := cfg.Overrides["claude-code"]
	want := []string{"step-1", "step-2", "step-3"}
	if len(o.PostInstall) != len(want) {
		t.Fatalf("order %+v, want %+v", o.PostInstall, want)
	}
	for i, s := range want {
		if o.PostInstall[i] != s {
			t.Fatalf("postInstall[%d]=%q, want %q", i, o.PostInstall[i], s)
		}
	}
}
