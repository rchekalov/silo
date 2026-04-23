// SPDX-License-Identifier: Apache-2.0

package version

// Version is the silo release string shown by `silo --version`.
// Overridable at link time via:
//
//	go build -ldflags "-X github.com/rchekalov/silo/internal/version.Version=<tag>"
var Version = "0.4.19"
