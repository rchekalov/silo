// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"strings"
	"testing"
)

func TestLookupLanguageAddonKotlin(t *testing.T) {
	a, ok := LookupLanguageAddon("kotlin")
	if !ok {
		t.Fatal("kotlin addon should exist in language_addons.yaml")
	}
	if a.Label == "" {
		t.Fatal("label missing")
	}
	if len(a.Steps) == 0 {
		t.Fatal("steps missing")
	}
	// Kotlin needs a JDK and the kotlin compiler; both must show up somewhere.
	joined := strings.Join(a.Steps, "\n")
	if !strings.Contains(joined, "kotlin") {
		t.Fatalf("kotlin not in steps: %s", joined)
	}
	if !strings.Contains(joined, "openjdk") {
		t.Fatalf("jdk not in steps: %s", joined)
	}
}

func TestLookupLanguageAddonUnknown(t *testing.T) {
	if _, ok := LookupLanguageAddon("nonexistent"); ok {
		t.Fatal("unknown language should return false")
	}
}

func TestPostInstallStepsDetaches(t *testing.T) {
	a := LanguageAddon{Steps: []string{"step-a", "step-b"}}
	got := a.PostInstallSteps()
	if len(got) != 2 || got[0] != "step-a" || got[1] != "step-b" {
		t.Fatalf("got %+v", got)
	}
	got[0] = "mutated"
	if a.Steps[0] == "mutated" {
		t.Fatal("PostInstallSteps must return a detached copy")
	}
}

func TestPostInstallStepsEmpty(t *testing.T) {
	if (LanguageAddon{}).PostInstallSteps() != nil {
		t.Fatal("empty steps should return nil")
	}
}

func TestLanguageAddonNamesIncludeKotlin(t *testing.T) {
	names := LanguageAddonNames()
	found := false
	for _, n := range names {
		if n == "kotlin" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("kotlin not among addon names: %+v", names)
	}
}
