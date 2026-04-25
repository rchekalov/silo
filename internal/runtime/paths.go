// SPDX-License-Identifier: Apache-2.0

// Package runtime exposes the layout of ~/.silo/ used by every subsystem.
// Every path should be obtained through a helper here rather than reconstructed
// ad-hoc by callers, so the layout stays in one place.
package runtime

import (
	"os"
	"path/filepath"
)

// Root returns ~/.silo. Panics if the home directory cannot be resolved (the
// rest of the tool cannot function without one, so this is a hard failure).
func Root() string {
	home, err := os.UserHomeDir()
	if err != nil {
		panic("silo: could not determine home directory: " + err.Error())
	}
	return filepath.Join(home, ".silo")
}

func Config() string     { return filepath.Join(Root(), "config.yaml") }
func ImageState() string { return filepath.Join(Root(), "state.json") }
func ShimBin() string    { return filepath.Join(Root(), "bin") }
func Kernel() string     { return filepath.Join(Root(), "vmlinux") }
func Initfs() string     { return filepath.Join(Root(), "initfs.ext4") }
func ImageStore() string { return filepath.Join(Root(), "images") }

// ContentStore is where Apple Containerization actually writes pulled OCI
// blobs (content-addressable, under `blobs/sha256/`). ImageStore() above is
// historical — we created it early and kept the name, but the framework
// picked "content" internally. pullprogress measures against this path.
func ContentStore() string   { return filepath.Join(Root(), "content") }
func Containers() string     { return filepath.Join(Root(), "containers") }
func Cache() string          { return filepath.Join(Root(), "cache") }
func Logs() string           { return filepath.Join(Root(), "logs") }
func RootfsCache() string    { return filepath.Join(Root(), "rootfs-cache") }
func Builds() string         { return filepath.Join(Root(), "builds") }
func GlobalSiloconf() string { return filepath.Join(Root(), "siloconf") }
func LocalDownloads() string { return filepath.Join(Root(), ".local") }
func UserRegistry() string   { return filepath.Join(Root(), "registry.yaml") }

// GlobalBuildRootfs is the path to ~/.silo/builds/<tool>/rootfs.ext4.
func GlobalBuildRootfs(tool string) string {
	return filepath.Join(Builds(), tool, "rootfs.ext4")
}

// ProjectRootfs is <projectRoot>/.silo/<tool>/rootfs.ext4 — the path where
// user-driven `silo build` writes its output. Auto-bakes from .siloconf no
// longer use this path; they live under BakedRootfs (content-addressed).
// Migrating `silo build` outputs to a centralized location is a follow-up.
func ProjectRootfs(projectRoot, tool string) string {
	return filepath.Join(projectRoot, ".silo", tool, "rootfs.ext4")
}

// Baked is ~/.silo/baked — content-addressed storage for project-baked rootfs
// images. Each subdirectory is named for the recipe hash (image + post-install
// steps), so two projects with identical recipes share a single ext4 file and
// `mv`-ing a project never invalidates its bake.
func Baked() string { return filepath.Join(Root(), "baked") }

// BakedDir is ~/.silo/baked/<recipeHash> — directory holding the rootfs and
// manifest. Useful for size accounting and removal.
func BakedDir(recipeHash string) string {
	return filepath.Join(Baked(), recipeHash)
}

// BakedRootfs is ~/.silo/baked/<recipeHash>/rootfs.ext4.
func BakedRootfs(recipeHash string) string {
	return filepath.Join(BakedDir(recipeHash), "rootfs.ext4")
}

// BakedManifest is ~/.silo/baked/<recipeHash>/manifest.json — a small sidecar
// recording what produced this rootfs (tool, image, steps, createdAt) for
// auditability and `silo projects` output.
func BakedManifest(recipeHash string) string {
	return filepath.Join(BakedDir(recipeHash), "manifest.json")
}

// Projects is ~/.silo/projects — per-project metadata directories keyed by
// either an explicit project_id (from .siloconf) or a hash of the project
// path. See runtime.ProjectID and runtime.LoadOrCreateMeta.
func Projects() string { return filepath.Join(Root(), "projects") }

// ProjectStateDir is ~/.silo/projects/<id>.
func ProjectStateDir(id string) string { return filepath.Join(Projects(), id) }

// ProjectMetaPath is ~/.silo/projects/<id>/meta.json.
func ProjectMetaPath(id string) string {
	return filepath.Join(ProjectStateDir(id), "meta.json")
}

// EnsureDirectories creates the standard ~/.silo subtree (idempotent).
func EnsureDirectories() error {
	for _, d := range []string{
		Root(), ShimBin(), ImageStore(), Containers(), Cache(), Logs(),
		RootfsCache(), Builds(), Baked(), Projects(),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}
