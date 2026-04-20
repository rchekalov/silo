// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"fmt"
	"os"

	"github.com/rchekalov/silo/internal/runtime"
	"github.com/spf13/cobra"
)

var resetForce bool

var resetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Delete ~/.silo and start fresh",
	RunE: func(cmd *cobra.Command, args []string) error {
		root := runtime.Root()
		if !resetForce {
			ok, err := Prompter.Confirm(
				fmt.Sprintf("This will delete %s. Type YES to confirm:", root),
				"YES",
			)
			if err != nil {
				return err
			}
			if !ok {
				fmt.Fprintln(os.Stderr, "Aborted.")
				return nil
			}
		}
		if err := os.RemoveAll(root); err != nil {
			return err
		}
		fmt.Printf("Removed %s\n", root)
		return nil
	},
}

func init() {
	resetCmd.Flags().BoolVar(&resetForce, "force", false, "skip confirmation prompt")
	addCommand(resetCmd)
}
