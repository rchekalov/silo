// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"fmt"
	"os"

	"github.com/rchekalov/silo/internal/runtime"
	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check runtime readiness (kernel, initfs, bootstrap)",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("Runtime:")
		fmt.Printf("  kernel: %s %s\n", runtime.Kernel(), existsMarker(runtime.Kernel()))
		fmt.Printf("  initfs: %s %s\n", runtime.Initfs(), existsMarker(runtime.Initfs()))
		return nil
	},
}

func init() { addCommand(doctorCmd) }

func existsMarker(p string) string {
	if _, err := os.Stat(p); err != nil {
		return "(not found)"
	}
	return "(ready)"
}
