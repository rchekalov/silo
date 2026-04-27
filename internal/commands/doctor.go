// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"fmt"
	"os"
	"sort"

	"github.com/spf13/cobra"

	"github.com/rchekalov/silo/internal/config"
	"github.com/rchekalov/silo/internal/runtime"
	"github.com/rchekalov/silo/internal/tools"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check runtime readiness (kernel, initfs, bootstrap)",
	RunE: func(_ *cobra.Command, _ []string) error {
		fmt.Println("Runtime:")
		fmt.Printf("  kernel: %s %s\n", runtime.Kernel(), existsMarker(runtime.Kernel()))
		fmt.Printf("  initfs: %s %s\n", runtime.Initfs(), existsMarker(runtime.Initfs()))

		// Surface PATH-ordering issues for already-installed tools — a
		// silo-shipped `pip` is useless if homebrew's pip outranks it.
		cfg, err := config.LoadGlobalConfig()
		if err == nil && len(cfg.Tools) > 0 {
			names := make([]string, 0, len(cfg.Tools))
			for n := range cfg.Tools {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				tools.WarnIfShimsShadowed(cfg.Tools[n], os.Stderr)
			}
		}
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
