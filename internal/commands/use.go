// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/rchekalov/silo/internal/config"
	"github.com/rchekalov/silo/internal/errs"
	"github.com/rchekalov/silo/internal/runtime"
	"github.com/rchekalov/silo/internal/tools"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var useGlobal bool

var useCmd = &cobra.Command{
	Use:   "use <tool>[@<version>]",
	Short: "Pin a tool for this project (writes .siloconf)",
	Long: `Record a dependency on <tool> in the project's .siloconf, so that
'silo sync' will install it and 'silo run <tool>' uses the chosen version.

  silo use python@3.12         # pins python 3.12 for the current project
  silo use node                # pins the default node version
  silo use --global python@3.12 # pins in ~/.silo/siloconf instead

If the requested version differs from what's installed globally, an image
override is recorded under 'overrides:'. Otherwise the tool is simply listed
under 'tools:'. The next 'silo sync' actually installs and pulls.`,
	Args: cobra.ExactArgs(1),
	RunE: runUse,
}

var unuseCmd = &cobra.Command{
	Use:   "unuse <tool>",
	Short: "Unpin a tool from this project (edits .siloconf)",
	Args:  cobra.ExactArgs(1),
	RunE:  runUnuse,
}

func init() {
	useCmd.Flags().BoolVar(&useGlobal, "global", false, "edit ~/.silo/siloconf instead of the project .siloconf")
	unuseCmd.Flags().BoolVar(&useGlobal, "global", false, "edit ~/.silo/siloconf instead of the project .siloconf")
	addCommand(useCmd)
	addCommand(unuseCmd)
}

func runUse(cmd *cobra.Command, args []string) error {
	name, version, err := tools.ParseSpec(args[0])
	if err != nil {
		return err
	}

	// Resolve the requested tool definition from the registry so we know the
	// image ref for comparison. If the tool isn't in the registry we can't
	// resolve an override image — but pinning a bare name still works.
	newDef, registered, err := tools.Lookup(name, version)
	if err != nil {
		return err
	}
	if version != "" && !registered {
		return errs.ToolNotFoundError(name)
	}

	cfg, target, err := loadEditableConfig()
	if err != nil {
		return err
	}

	cfg.AddTool(name)

	// Decide whether to record an image override. Only set one if we have a
	// concrete image to pin AND it differs from what's installed globally. If
	// the tool isn't installed yet, 'silo sync' will install newDef directly,
	// so no override is needed.
	global, err := config.LoadGlobalConfig()
	if err != nil {
		return err
	}
	if registered && newDef.Image != "" {
		if installed, ok := global.Tools[name]; ok && installed.Image != newDef.Image {
			cfg.SetOverrideImage(name, newDef.Image)
		}
	}

	savedPath, err := saveEditableConfig(cfg, target)
	if err != nil {
		return err
	}
	scope := "project"
	if useGlobal {
		scope = "global"
	}
	fmt.Fprintf(os.Stderr, "Pinned %s (%s). Run `silo sync` to install.\n", args[0], scope)
	fmt.Fprintf(os.Stderr, "Wrote %s\n", savedPath)
	return nil
}

func runUnuse(cmd *cobra.Command, args []string) error {
	name := args[0]
	cfg, target, err := loadEditableConfig()
	if err != nil {
		return err
	}
	if !cfg.RemoveTool(name) {
		return fmt.Errorf("%q is not pinned in %s", name, editablePath(target))
	}
	savedPath, err := saveEditableConfig(cfg, target)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Unpinned %q from %s\n", name, savedPath)
	return nil
}

// loadEditableConfig returns the ProjectConfig the user is about to edit and
// the directory to save it back to. With --global the directory is ~/.silo;
// otherwise it's the nearest .siloconf walking up from cwd, falling back to a
// fresh config rooted at cwd so `silo use` works without a prior `silo init`.
func loadEditableConfig() (*config.ProjectConfig, string, error) {
	if useGlobal {
		root := runtime.Root()
		if err := os.MkdirAll(root, 0o755); err != nil {
			return nil, "", err
		}
		cfg, err := config.LoadGlobalSiloconf()
		if err != nil {
			return nil, "", err
		}
		if cfg == nil {
			cfg = &config.ProjectConfig{}
		}
		return cfg, root, nil
	}
	return config.FindOrDefault()
}

// editablePath is the file path implied by loadEditableConfig's second return.
// The global siloconf lives at ~/.silo/siloconf (no dot), unlike project-level
// .siloconf — so we can't just use ProjectConfig.Save(dir) for both cases.
func editablePath(dir string) string {
	if useGlobal {
		return runtime.GlobalSiloconf()
	}
	return filepath.Join(dir, config.ProjectConfigFilename)
}

// saveEditableConfig writes cfg to either the project-level .siloconf (via
// ProjectConfig.Save) or the global ~/.silo/siloconf. Returns the path written.
func saveEditableConfig(cfg *config.ProjectConfig, dir string) (string, error) {
	if useGlobal {
		path := runtime.GlobalSiloconf()
		out, err := yaml.Marshal(cfg)
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(path, out, 0o644); err != nil {
			return "", err
		}
		return path, nil
	}
	if err := cfg.Save(dir); err != nil {
		return "", err
	}
	return filepath.Join(dir, config.ProjectConfigFilename), nil
}
