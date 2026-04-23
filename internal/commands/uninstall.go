// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/rchekalov/silo/internal/bridge"
	"github.com/rchekalov/silo/internal/cache"
	"github.com/rchekalov/silo/internal/config"
	"github.com/rchekalov/silo/internal/runtime"
	"github.com/rchekalov/silo/internal/shim"
	"github.com/rchekalov/silo/internal/tools"
)

var uninstallKeepImage bool

var uninstallCmd = &cobra.Command{
	Use:   "uninstall <tool>",
	Short: "Uninstall a tool and remove its shims",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		name := args[0]
		cfg, err := config.LoadGlobalConfig()
		if err != nil {
			return err
		}
		def, installed := cfg.Tools[name]

		installer := &tools.Installer{
			Config:   cfg,
			Shims:    shim.NewManager(""),
			Prompter: Prompter,
		}
		if err := installer.Uninstall(name); err != nil {
			return err
		}
		fmt.Printf("Uninstalled %q.\n", name)

		if !installed || uninstallKeepImage {
			return nil
		}
		// Best-effort reclamation: rootfs cache entry + OCI image. Don't fail
		// the command if the runtime isn't available (e.g., fresh install
		// where the bridge can't bootstrap).
		freeUninstalledArtifacts(name, def, cfg)
		return nil
	},
}

// freeUninstalledArtifacts removes the rootfs cache entry and (if unshared)
// the OCI image reference for a just-uninstalled tool.
func freeUninstalledArtifacts(name string, def config.ToolDefinition, remaining *config.GlobalConfig) {
	mgr, err := bridge.NewManager(runtime.Kernel(), runtime.Initfs(), runtime.Root(), false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "note: skipped rootfs/image reclamation (runtime unavailable: %v)\n", err)
		return
	}
	defer mgr.Close()

	img, err := mgr.ImageGet(def.Image, false)
	if err != nil {
		// No local image. Nothing to free.
		return
	}
	digest := img.Digest()
	img.Close()

	// Free the rootfs cache entry (always safe — it can always be re-derived).
	if err := cache.NewRootfs("").RemoveByDigest(digest, 0); err == nil {
		fmt.Fprintf(os.Stderr, "Freed rootfs cache entry for %s.\n", name)
	}

	// Is the image shared with another installed tool? If so, keep it.
	shared := false
	if remaining != nil {
		for otherName, otherDef := range remaining.Tools {
			if otherName == name {
				continue
			}
			if otherDef.Image == def.Image {
				shared = true
				break
			}
		}
	}
	if shared {
		fmt.Fprintf(os.Stderr, "Kept image %s (shared with another tool).\n", def.Image)
		return
	}

	if err := mgr.ImageDelete(def.Image, true); err != nil {
		fmt.Fprintf(os.Stderr, "warning: image_delete(%s): %v\n", def.Image, err)
		return
	}
	fmt.Fprintf(os.Stderr, "Deleted image %s (and GC'd orphan blobs).\n", def.Image)
}

func init() {
	uninstallCmd.Flags().BoolVar(&uninstallKeepImage, "keep-image", false, "keep the OCI image and rootfs cache after uninstall")
	addCommand(uninstallCmd)
}
