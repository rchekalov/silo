// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"os"
	"path/filepath"
)

// DetectedTool is a tool recognized by marker files in a directory.
type DetectedTool struct {
	Name    string   // registry tool name
	Markers []string // marker files that triggered the match (e.g. ["package.json"])
}

// DetectedAddon is a language recognized by marker files that doesn't map to
// a first-class silo tool. Addons extend an existing tool's rootfs (typically
// claude-code) with extra packages via .siloconf postInstall — they never go
// into `tools:` because they have no VM of their own.
type DetectedAddon struct {
	Name    string   // language key (kotlin, java, ruby…)
	Markers []string // marker files that triggered the match
}

// markerMap maps tool names to their marker files. Order of iteration matches
// the Swift implementation's detection order.
var markerMap = []struct {
	Tool  string
	Files []string
}{
	{"python", []string{"requirements.txt", "pyproject.toml", "setup.py", "Pipfile"}},
	{"node", []string{"package.json"}},
	{"rust", []string{"Cargo.toml"}},
	{"go", []string{"go.mod"}},
	{"deno", []string{"deno.json", "deno.jsonc"}},
}

// addonMarkerMap is the language-level version of markerMap. Language names
// here must match keys in language_addons.yaml so `silo init` / `silo add`
// can look up the apt packages to install.
var addonMarkerMap = []struct {
	Name  string
	Files []string
}{
	{"kotlin", []string{"build.gradle.kts", "settings.gradle.kts"}},
	{"java", []string{"pom.xml", "build.gradle"}},
	{"ruby", []string{"Gemfile"}},
}

// defaultExcludes lists directories typically excluded from workspace mounts
// for each tool. Used by `silo init` to pre-fill mount.exclude.
var defaultExcludes = map[string][]string{
	"python": {".venv", "__pycache__"},
	"node":   {"node_modules"},
	"rust":   {"target"},
	"go":     nil,
	"deno":   nil,
}

// Detect scans `dir` for marker files and returns every matched tool, in the
// order of markerMap. Directories are not scanned — only direct entries.
func Detect(dir string) []DetectedTool {
	if dir == "" {
		dir, _ = os.Getwd()
	}
	var out []DetectedTool
	for _, entry := range markerMap {
		var found []string
		for _, f := range entry.Files {
			if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
				found = append(found, f)
			}
		}
		if len(found) > 0 {
			out = append(out, DetectedTool{Name: entry.Tool, Markers: found})
		}
	}
	return out
}

// DetectAddons scans `dir` for language-addon markers (Kotlin, Java, Ruby…)
// and returns every matched language. Addons are distinct from first-class
// tools: they don't get their own VM, they extend a host tool's rootfs via
// project-level postInstall.
func DetectAddons(dir string) []DetectedAddon {
	if dir == "" {
		dir, _ = os.Getwd()
	}
	var out []DetectedAddon
	for _, entry := range addonMarkerMap {
		var found []string
		for _, f := range entry.Files {
			if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
				found = append(found, f)
			}
		}
		if len(found) > 0 {
			out = append(out, DetectedAddon{Name: entry.Name, Markers: found})
		}
	}
	return out
}

// CollectExcludes returns the deduplicated union of default excludes for the
// given tools. Empty for any tool with no excludes.
func CollectExcludes(tools []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, t := range tools {
		for _, ex := range defaultExcludes[t] {
			if _, ok := seen[ex]; ok {
				continue
			}
			seen[ex] = struct{}{}
			out = append(out, ex)
		}
	}
	return out
}
