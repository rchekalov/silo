// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:        "status",
	Short:      "Deprecated: see `silo doctor`, `silo current`, `silo cache report`",
	Deprecated: "use `silo doctor` for runtime readiness, `silo current` for installed tools and overrides, `silo cache report` for cache usage.",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintln(os.Stderr, "note: `silo status` is split into `silo doctor` + `silo current` + `silo cache report`; status will be removed in 0.6.0.")
		if err := doctorCmd.RunE(cmd, args); err != nil {
			return err
		}
		fmt.Println()
		return currentCmd.RunE(cmd, args)
	},
}

func init() { addCommand(statusCmd) }
