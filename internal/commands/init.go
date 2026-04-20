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

		c := &config.ProjectConfig{}
		if excludes := tools.CollectExcludes(selected); len(excludes) > 0 {
			c.Mount = &config.MountConfig{Exclude: excludes}
		}
		if err := c.Save(cwd); err != nil {
			return err
		}
		_ = appendToGitignore(filepath.Join(cwd, ".gitignore"), ".silo/")

		fmt.Printf("Created .siloconf in %s\n", cwd)
		if len(selected) > 0 {
			fmt.Printf("Detected tools: %s\n", strings.Join(selected, ", "))
		}
		return nil
	},
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
