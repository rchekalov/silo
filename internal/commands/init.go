// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rchekalov/silo/internal/config"
	"github.com/rchekalov/silo/internal/tools"
	"github.com/spf13/cobra"
)

var (
	initNoInteractive bool
	initTools         []string
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Create .siloconf in the current directory",
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		target := filepath.Join(cwd, config.ProjectConfigFilename)
		if _, err := os.Stat(target); err == nil {
			return fmt.Errorf("%s already exists", target)
		}

		// Pick the set of tools: explicit --tool flag wins, else auto-detect.
		var selected []string
		switch {
		case len(initTools) > 0:
			selected = initTools
		default:
			detected := tools.Detect(cwd)
			if len(detected) > 0 && !initNoInteractive {
				choices := make([]string, 0, len(detected))
				for _, d := range detected {
					choices = append(choices, fmt.Sprintf("%s (found %s)", d.Name, strings.Join(d.Markers, ", ")))
				}
				if ok, _ := Prompter.AskYesNo("Detected project tools. Include them in .siloconf?", true); ok {
					for _, d := range detected {
						selected = append(selected, d.Name)
					}
				}
			} else if len(detected) > 0 {
				// --no-interactive but markers exist: take them all.
				for _, d := range detected {
					selected = append(selected, d.Name)
				}
			}
		}

		c := &config.ProjectConfig{Tools: selected}
		if excludes := tools.CollectExcludes(selected); len(excludes) > 0 {
			c.Mount = &config.MountConfig{Exclude: excludes}
		}

		// Language-level addons (Kotlin, Java, Ruby): not first-class tools, so
		// they don't belong in `tools:`. Instead, suggest baking them into an
		// installed host tool (claude-code) via overrides.<tool>.postInstall.
		addonNotes := maybeAddLanguageAddons(c, cwd, initNoInteractive)

		if err := c.Save(cwd); err != nil {
			return err
		}
		_ = appendToGitignore(filepath.Join(cwd, ".gitignore"), ".silo/")

		fmt.Printf("Created .siloconf in %s\n", cwd)
		if len(selected) > 0 {
			fmt.Printf("Detected tools: %s\n", strings.Join(selected, ", "))
		}
		for _, note := range addonNotes {
			fmt.Println(note)
		}
		return nil
	},
}

// maybeAddLanguageAddons detects language-only markers (Kotlin, Java, Ruby)
// that don't map to a first-class silo tool, and — if claude-code is
// installed — records the corresponding postInstall step under
// overrides.claude-code. Returns human-readable notes to print after save.
// Silent when there are no addons, claude-code is not installed, or the
// user declines interactively.
func maybeAddLanguageAddons(c *config.ProjectConfig, cwd string, noInteractive bool) []string {
	addons := tools.DetectAddons(cwd)
	if len(addons) == 0 {
		return nil
	}
	global, err := config.LoadGlobalConfig()
	if err != nil || global == nil {
		return nil
	}
	hostTool := "claude-code"
	if _, ok := global.Tools[hostTool]; !ok {
		// Claude-code isn't installed, so there's no host rootfs to extend.
		// Print a hint instead of silently skipping.
		notes := make([]string, 0, len(addons))
		for _, a := range addons {
			if addon, known := tools.LookupLanguageAddon(a.Name); known {
				notes = append(notes, fmt.Sprintf(
					"Detected %s project (%s). Install %s and run `silo add %s` to bake it in.",
					addon.Label, strings.Join(a.Markers, ", "), hostTool, a.Name,
				))
			}
		}
		return notes
	}

	var notes []string
	for _, a := range addons {
		addon, known := tools.LookupLanguageAddon(a.Name)
		if !known {
			continue
		}
		langSteps := addon.PostInstallSteps()
		if len(langSteps) == 0 {
			continue
		}
		include := false
		if noInteractive {
			include = true
		} else {
			question := fmt.Sprintf("Detected %s (%s). Add %s to %s for this project?",
				addon.Label, strings.Join(a.Markers, ", "), addon.Label, hostTool)
			if ok, _ := Prompter.AskYesNo(question, true); ok {
				include = true
			}
		}
		if !include {
			continue
		}
		if c.Overrides == nil {
			c.Overrides = map[string]config.ToolOverride{}
		}
		o := c.Overrides[hostTool]
		// Dedup — init should be rerunnable against a partial config in future.
		seen := map[string]struct{}{}
		for _, existing := range o.PostInstall {
			seen[existing] = struct{}{}
		}
		for _, step := range langSteps {
			if _, ok := seen[step]; ok {
				continue
			}
			o.PostInstall = append(o.PostInstall, step)
			seen[step] = struct{}{}
		}
		c.Overrides[hostTool] = o
		notes = append(notes, fmt.Sprintf("Added %s to overrides.%s.postInstall. Run `silo sync` to bake.",
			addon.Label, hostTool))
	}
	return notes
}

// appendToGitignore adds a line to .gitignore if the file exists and the entry isn't already present.
func appendToGitignore(path, entry string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if bytes.Contains(raw, []byte(entry)) {
		return nil
	}
	if len(raw) > 0 && raw[len(raw)-1] != '\n' {
		raw = append(raw, '\n')
	}
	raw = append(raw, []byte(entry+"\n")...)
	return os.WriteFile(path, raw, 0o644)
}

func init() {
	initCmd.Flags().StringSliceVar(&initTools, "tool", nil, "comma-separated tool names (skip detection)")
	initCmd.Flags().BoolVar(&initNoInteractive, "no-interactive", false, "don't prompt; auto-include detected tools")
	addCommand(initCmd)
}
