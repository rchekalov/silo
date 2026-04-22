// SPDX-License-Identifier: Apache-2.0

package tools

import (
	_ "embed"
	"fmt"

	"gopkg.in/yaml.v3"
)

//go:embed language_addons.yaml
var languageAddonsYAML []byte

// LanguageAddon describes what it takes to extend a host tool's rootfs with
// support for a specific language (Kotlin, Java, Ruby…). Steps are raw shell
// fragments executed in order; the assumption is a Debian-based base image
// (node:22-slim for claude-code), so callers can safely use apt-get.
//
// Keeping Steps opaque (vs. a structured `apt` field) lets addons mix
// package installs with curl/unzip or SDKMAN flows where a single apt line
// isn't enough — the Kotlin addon needs this because `kotlin` isn't in
// Debian's apt repository today.
type LanguageAddon struct {
	Label string   `yaml:"label"`
	Steps []string `yaml:"steps"`
}

// PostInstallSteps returns a copy of the addon's shell fragments, ready to be
// appended to a tool's postInstall. The slice is detached — callers can
// mutate without touching the cache.
func (a LanguageAddon) PostInstallSteps() []string {
	if len(a.Steps) == 0 {
		return nil
	}
	return append([]string(nil), a.Steps...)
}

type languageAddons struct {
	Addons map[string]LanguageAddon `yaml:"addons"`
}

var languageAddonCache *languageAddons

func loadLanguageAddons() (*languageAddons, error) {
	if languageAddonCache != nil {
		return languageAddonCache, nil
	}
	var parsed languageAddons
	if err := yaml.Unmarshal(languageAddonsYAML, &parsed); err != nil {
		return nil, fmt.Errorf("parse language_addons.yaml: %w", err)
	}
	languageAddonCache = &parsed
	return languageAddonCache, nil
}

// LookupLanguageAddon returns the addon registered for `name` (kotlin, java,
// ruby…). The second return is false when the name is unknown.
func LookupLanguageAddon(name string) (LanguageAddon, bool) {
	all, err := loadLanguageAddons()
	if err != nil {
		return LanguageAddon{}, false
	}
	a, ok := all.Addons[name]
	return a, ok
}

// LanguageAddonNames returns the registered addon keys. Map iteration order
// is unspecified — sort on the caller side when the ordering matters.
func LanguageAddonNames() []string {
	all, err := loadLanguageAddons()
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(all.Addons))
	for k := range all.Addons {
		out = append(out, k)
	}
	return out
}
