// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var shellenvCmd = &cobra.Command{
	Use:   "shellenv [bash|zsh|fish]",
	Short: `Print shell init for ~/.silo/bin — use via eval "$(silo shellenv)"`,
	Long: `Print the shell-specific command to add ~/.silo/bin to PATH so that
silo's tool shims (python, node, npm, ...) are found by the shell.

Usage — one-shot for the current shell:

    eval "$(silo shellenv)"

Usage — permanent (adds to your shell profile so every new shell picks it up):

    echo 'eval "$(silo shellenv)"' >> ~/.zshrc   # zsh
    echo 'eval "$(silo shellenv)"' >> ~/.bashrc  # bash
    echo 'silo shellenv fish | source' >> ~/.config/fish/config.fish

If no shell is supplied, silo detects it from $SHELL.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		shell := detectShell(args)
		fmt.Println(shellenvFor(shell))
		return nil
	},
}

func init() { addCommand(shellenvCmd) }

// detectShell resolves the shell name from explicit arg or $SHELL.
func detectShell(args []string) string {
	if len(args) == 1 {
		return args[0]
	}
	return filepath.Base(os.Getenv("SHELL"))
}

// shellenvFor returns the shell-specific PATH export. Unknown shells fall
// through to POSIX syntax, which works for bash/zsh/sh/dash.
func shellenvFor(shell string) string {
	switch shell {
	case "fish":
		return `set -gx PATH "$HOME/.silo/bin" $PATH`
	default:
		return `export PATH="$HOME/.silo/bin:$PATH"`
	}
}
