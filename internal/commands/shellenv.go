// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var shellenvCmd = &cobra.Command{
	Use:   "shellenv [bash|zsh|fish|csh|tcsh|pwsh]",
	Short: `Print shell init for ~/.silo/bin — use via eval "$(silo shellenv)"`,
	Long: `Print the shell-specific command to add ~/.silo/bin to PATH so that
silo's tool shims (python, node, npm, ...) are found by the shell.

Supported shells: bash, zsh, sh, ksh (POSIX default), fish, csh, tcsh, pwsh.
Unknown shell names fall back to POSIX syntax.

Usage — one-shot for the current shell:

    eval "$(silo shellenv)"              # bash / zsh / sh / ksh
    silo shellenv fish | source          # fish
    eval ` + "`silo shellenv csh`" + `         # csh / tcsh (backticks, not $(...))
    silo shellenv pwsh | Invoke-Expression   # pwsh

Usage — permanent (adds to your shell profile so every new shell picks it up):

    echo 'eval "$(silo shellenv)"' >> ~/.zshrc
    echo 'eval "$(silo shellenv)"' >> ~/.bashrc
    echo 'silo shellenv fish | source' >> ~/.config/fish/config.fish
    echo 'eval ` + "`silo shellenv csh`" + `' >> ~/.tcshrc

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
// through to POSIX syntax, which covers bash/zsh/sh/dash/ksh and any
// POSIX-compatible newcomer.
//
// fish: fish_add_path (fish 3.2+, Feb 2021) is idempotent — re-sourcing
// config.fish in the same session won't duplicate the entry — and is the
// fish-community-sanctioned way to edit PATH. A plain
// `set -gx PATH "$HOME/.silo/bin" $PATH` also works (fish preserves the
// exported flag across `set` without -x), but it grows PATH every reload
// and is more brittle for users who know fish but haven't internalised the
// list-vs-string semantics.
//
// csh/tcsh: no `export`; `setenv PATH value` is the csh equivalent. csh
// automatically mirrors the colon-joined PATH to its list-valued $path, so
// the one-liner form is sufficient and works identically to the POSIX line.
//
// pwsh: the path separator differs by OS (":" on macOS/Linux, ";" on
// Windows). [IO.Path]::PathSeparator yields the right one portably — same
// trick `brew shellenv pwsh` uses.
func shellenvFor(shell string) string {
	switch shell {
	case "fish":
		return `fish_add_path --global --prepend "$HOME/.silo/bin"`
	case "csh", "tcsh":
		return `setenv PATH "$HOME/.silo/bin:$PATH";`
	case "pwsh", "powershell":
		return `$env:PATH = "$HOME/.silo/bin" + [IO.Path]::PathSeparator + $env:PATH`
	default:
		return `export PATH="$HOME/.silo/bin:$PATH"`
	}
}
