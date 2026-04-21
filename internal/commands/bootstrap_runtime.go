// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"fmt"
	"os"

	"github.com/rchekalov/silo/internal/engine"
	"github.com/rchekalov/silo/internal/runtime"
	"github.com/spf13/cobra"
)

// bootstrapRuntimeCmd is a hidden entry point used by the release workflow to
// produce ~/.silo/vmlinux and ~/.silo/initfs.ext4 without booting a VM or
// pulling any OCI images. It's intentionally separate from `silo install` so
// CI can run it on a clean runner that doesn't have Virtualization.framework
// access.
var bootstrapRuntimeCmd = &cobra.Command{
	Use:    "bootstrap-runtime",
	Short:  "Download or build the silo runtime (vmlinux + initfs.ext4).",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := engine.EnsureRuntime(); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "kernel: %s\ninitfs: %s\n", runtime.Kernel(), runtime.Initfs())
		return nil
	},
}

func init() { addCommand(bootstrapRuntimeCmd) }
