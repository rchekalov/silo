// SPDX-License-Identifier: Apache-2.0

package bridge

// MountSpec describes a single container mount. Use Block or Share helpers.
type MountSpec struct {
	Type        string   // "block" or "share"
	Format      string   // "ext4" for block; empty for share
	Source      string
	Destination string
	Options     []string
}

// Block returns an ext4 block mount.
func Block(source, destination string) MountSpec {
	return MountSpec{Type: "block", Format: "ext4", Source: source, Destination: destination}
}

// Share returns a shared-folder mount (virtio-fs).
func Share(source, destination string) MountSpec {
	return MountSpec{Type: "share", Source: source, Destination: destination}
}

// WithOptions attaches mount options (e.g., "ro"). Returns a new value.
func (m MountSpec) WithOptions(opts ...string) MountSpec {
	m.Options = append([]string(nil), opts...)
	return m
}

// HostEntry is one line of /etc/hosts inside the container.
type HostEntry struct {
	IPAddress string
	Hostnames []string
}

// ContainerConfig mirrors SBContainerConfig. All fields are snake-case in C;
// here they use Go idioms. Convert via marshalContainerConfig.
type ContainerConfig struct {
	CPUs             int32
	MemoryBytes      uint64
	Arguments        []string
	WorkingDirectory string
	EnvVars          []string // "KEY=VALUE"
	Mounts           []MountSpec
	StdinFD          int32 // -1 = not set
	StdoutFD         int32 // -1 = not set
	StderrFD         int32 // -1 = not set
	UseTerminal      bool
	EnableNetworking bool
	DNSNameservers   []string
	HostEntries      []HostEntry
	AutoInjectHost   bool // inject gateway IP as "host.silo.internal"
}

// ExecConfig mirrors SBExecConfig.
type ExecConfig struct {
	Arguments        []string
	WorkingDirectory string
	StdinFD          int32
	StdoutFD         int32
	StderrFD         int32
	UseTerminal      bool
}

// DefaultContainerConfig returns a config with sensible defaults:
// 2 CPUs, 512 MiB RAM, no stdio redirection, no terminal.
func DefaultContainerConfig() ContainerConfig {
	return ContainerConfig{
		CPUs:        2,
		MemoryBytes: 512 * 1024 * 1024,
		StdinFD:     -1,
		StdoutFD:    -1,
		StderrFD:    -1,
	}
}
