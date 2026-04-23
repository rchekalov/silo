// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"github.com/spf13/cobra"

	"github.com/rchekalov/silo/internal/prompter"
	"github.com/rchekalov/silo/internal/version"
)

// Prompter is the user-input abstraction shared by every command. Tests can
// replace it with a prompter.Scripted. Production code uses the terminal
// prompter which reads stdin and writes prompts to stderr.
var Prompter prompter.Prompter = prompter.NewTerminal()

var rootCmd = &cobra.Command{
	Use:           "silo",
	Short:         "Run dev tools inside isolated Apple Container micro-VMs",
	Version:       version.Version,
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute runs the root command and returns any error.
func Execute() error {
	return rootCmd.Execute()
}

// AddCommand is a small helper used by per-command files' init() funcs.
func addCommand(c *cobra.Command) {
	rootCmd.AddCommand(c)
}
