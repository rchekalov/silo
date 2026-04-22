// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/rchekalov/silo/internal/config"
	"github.com/rchekalov/silo/internal/errs"
	"github.com/rchekalov/silo/internal/tools"
	"github.com/spf13/cobra"
)

var (
	addForTool string
	addStep    string
	addNoSync  bool
)

var addCmd = &cobra.Command{
	Use:   "add [package | language ...]",
	Short: "Add packages to a tool's project-scoped rootfs",
	Long: `Extend a tool's rootfs with extra packages for this project.

Arguments are treated as apt package names, except when an argument matches
a known language shortcut (kotlin, java, ruby) — in which case it expands
to that language's default package set. Each addition is recorded in
.siloconf under overrides.<tool>.postInstall, and a fresh bake produces
<projectRoot>/.silo/<tool>/rootfs.ext4 on exit.

Default target is claude-code; override with --for <tool>. The base image
must be Debian-based (apt-get available) — holds for node:22-slim, the
default claude-code base.

Examples:
  silo add kotlin                          # JDK + Kotlin into claude-code
  silo add ripgrep jq                      # arbitrary apt packages
  silo add --for node --step 'npm install -g typescript'
`,
	Args: cobra.MinimumNArgs(0),
	RunE: runAdd,
}

func init() {
	addCmd.Flags().StringVar(&addForTool, "for", "", "tool to extend (default: claude-code if installed)")
	addCmd.Flags().StringVar(&addStep, "step", "", "raw shell fragment to append as a postInstall step (instead of apt packages)")
	addCmd.Flags().BoolVar(&useGlobal, "global", false, "edit ~/.silo/siloconf instead of the project .siloconf")
	addCmd.Flags().BoolVar(&addNoSync, "no-sync", false, "record the change in .siloconf but skip the bake")
	addCommand(addCmd)
}

func runAdd(cmd *cobra.Command, args []string) error {
	if len(args) == 0 && addStep == "" {
		return errs.Configf("nothing to add — pass package names or --step <shell>")
	}

	tool, err := resolveAddTargetTool()
	if err != nil {
		return err
	}

	newSteps, summary, err := stepsFromAddArgs(args, addStep)
	if err != nil {
		return err
	}

	cfg, target, err := loadEditableConfig()
	if err != nil {
		return err
	}
	if err := appendPostInstallSteps(cfg, tool, newSteps); err != nil {
		return err
	}
	savedPath, err := saveEditableConfig(cfg, target)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Wrote %s (tool=%s, +%d step)\n", savedPath, tool, len(newSteps))
	for _, s := range summary {
		fmt.Fprintf(os.Stderr, "  + %s\n", s)
	}

	if useGlobal || addNoSync {
		if useGlobal {
			fmt.Fprintf(os.Stderr, "global edit — project-scoped bake skipped. Run `silo build %s -- ...` to apply globally.\n", tool)
		} else {
			fmt.Fprintln(os.Stderr, "--no-sync set. Run `silo sync` to bake.")
		}
		return nil
	}

	return bakeAfterAdd(tool)
}

// resolveAddTargetTool figures out which tool the add operation targets.
// Falls back to claude-code when --for is not supplied.
func resolveAddTargetTool() (string, error) {
	if addForTool != "" {
		if _, reserved := tools.ReservedNames[addForTool]; reserved {
			return "", errs.Configf("%q is a reserved subcommand", addForTool)
		}
		return addForTool, nil
	}
	return "claude-code", nil
}

// stepsFromAddArgs turns argv + --step into (postInstall entries, human-readable summary).
// Language-matching args become addon-specific steps; the remainder is collapsed
// into one apt-get install line. --step is appended verbatim last.
func stepsFromAddArgs(args []string, extraStep string) ([]string, []string, error) {
	var steps []string
	var summary []string

	// Split args into language addons and raw apt packages.
	var aptPkgs []string
	for _, a := range args {
		if addon, ok := tools.LookupLanguageAddon(a); ok {
			langSteps := addon.PostInstallSteps()
			if len(langSteps) == 0 {
				return nil, nil, errs.Configf("language %q has no steps configured", a)
			}
			steps = append(steps, langSteps...)
			summary = append(summary, fmt.Sprintf("language %s (%s)", a, addon.Label))
			continue
		}
		aptPkgs = append(aptPkgs, a)
	}
	if len(aptPkgs) > 0 {
		sort.Strings(aptPkgs)
		step := "apt-get update && apt-get install -y --no-install-recommends " +
			strings.Join(aptPkgs, " ") +
			" && rm -rf /var/lib/apt/lists/*"
		steps = append(steps, step)
		summary = append(summary, fmt.Sprintf("apt: %s", strings.Join(aptPkgs, ", ")))
	}
	if extraStep != "" {
		steps = append(steps, extraStep)
		summary = append(summary, "step: "+extraStep)
	}
	if len(steps) == 0 {
		return nil, nil, errs.Configf("nothing to add")
	}
	return steps, summary, nil
}

// appendPostInstallSteps merges newSteps into cfg.Overrides[tool].PostInstall,
// skipping entries that are already present (idempotent). Order is preserved:
// previously-recorded steps first, then new additions.
func appendPostInstallSteps(cfg *config.ProjectConfig, tool string, newSteps []string) error {
	if cfg.Overrides == nil {
		cfg.Overrides = map[string]config.ToolOverride{}
	}
	o := cfg.Overrides[tool]
	existing := map[string]struct{}{}
	for _, s := range o.PostInstall {
		existing[s] = struct{}{}
	}
	for _, s := range newSteps {
		if _, ok := existing[s]; ok {
			continue
		}
		o.PostInstall = append(o.PostInstall, s)
		existing[s] = struct{}{}
	}
	cfg.Overrides[tool] = o
	return nil
}

// bakeAfterAdd runs the project-scoped bake for `tool` after a successful add.
// Reuses `silo sync`'s bake machinery so the two surfaces stay in lockstep.
func bakeAfterAdd(tool string) error {
	ws, err := config.ResolveWorkspace("")
	if err != nil {
		return err
	}
	if ws.Merged == nil || ws.ProjectRoot == "" {
		return errs.Configf("cannot bake: no .siloconf found after edit")
	}
	global, err := config.LoadGlobalConfig()
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Baking %s...\n", tool)
	return bakeProjectPostInstallFor(tool, ws.Merged, global, ws.ProjectRoot)
}
