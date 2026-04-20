// SPDX-License-Identifier: Apache-2.0

package config

import "os"

// Workspace is the merged view of a silo invocation's configuration context:
// any .siloconf walked up from the start directory, merged over
// ~/.silo/siloconf (project wins). It's a thin wrapper around
// FindMergedProjectConfig that lets callers share one resolution path instead
// of each computing project root, cwd fallback, and error handling on their
// own — that drift is what let a project-scoped build silently target the
// global artifact.
type Workspace struct {
	Merged      *ProjectConfig
	ProjectRoot string
}

// ResolveWorkspace walks up from start (or cwd if start == "") looking for
// .siloconf, merges it over ~/.silo/siloconf, and surfaces any parse errors.
// Returns an empty Workspace when neither config is present.
func ResolveWorkspace(start string) (Workspace, error) {
	merged, root, err := FindMergedProjectConfig(start)
	if err != nil {
		return Workspace{}, err
	}
	return Workspace{Merged: merged, ProjectRoot: root}, nil
}

// ProjectDir returns ProjectRoot if set, otherwise the current working
// directory. Callers that previously wrote
// `if projectRoot == "" { projectRoot, _ = os.Getwd() }` use this.
func (w Workspace) ProjectDir() (string, error) {
	if w.ProjectRoot != "" {
		return w.ProjectRoot, nil
	}
	return os.Getwd()
}
