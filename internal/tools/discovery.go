// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"bytes"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"github.com/rchekalov/silo/internal/config"
)

// CaptureRunFunc is the engine hook Discovery needs: run `command arguments`
// inside a VM for `tool`, capturing stdout into `out`. Returns exit code.
// Passing a func rather than an engine.ContainerEngine avoids an import cycle.
type CaptureRunFunc func(toolName string, tool config.ToolDefinition, command string, arguments []string, out io.Writer) (int32, error)

// DiscoverExecutables boots a VM for `tool` and returns every non-blocklisted
// executable basename it finds in standard PATH directories. Used by
// `silo install --image <custom>` to auto-populate shims when the caller
// didn't pass --shim.
//
// Port of Sources/SiloCore/Build/ExecutableDiscovery.swift.
func DiscoverExecutables(run CaptureRunFunc, toolName string, tool config.ToolDefinition) ([]string, error) {
	// Cap resources so discovery is cheap — we only need `find`.
	tool.CPUs = 1
	tool.MemoryMB = 256
	if tool.Workdir == "" {
		tool.Workdir = "/"
	}
	findCmd := "find /usr/bin /usr/local/bin /usr/sbin /sbin -maxdepth 1 -type f -executable 2>/dev/null | sort"

	var buf bytes.Buffer
	exit, err := run(toolName, tool, "sh", []string{"-c", findCmd}, &buf)
	if err != nil {
		return nil, fmt.Errorf("discover %q: %w", tool.Image, err)
	}
	if exit != 0 {
		return nil, fmt.Errorf("discover %q: find exited %d", tool.Image, exit)
	}
	return parseDiscoveredLines(buf.Bytes()), nil
}

// parseDiscoveredLines extracts basenames from `find` output, dedupes, and
// filters via the blocklist.
func parseDiscoveredLines(output []byte) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, line := range bytes.Split(output, []byte{'\n'}) {
		l := strings.TrimSpace(string(line))
		if l == "" {
			continue
		}
		name := path.Base(l)
		if name == "" {
			continue
		}
		if _, blocked := shimBlocklist[name]; blocked {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// shimBlocklist is the set of system utility names that should never become
// shims. Matches the Swift implementation verbatim.
var shimBlocklist = map[string]struct{}{
	// Shells
	"sh": {}, "bash": {}, "dash": {}, "zsh": {}, "csh": {}, "tcsh": {}, "ash": {},
	// Core utilities
	"ls": {}, "cat": {}, "cp": {}, "mv": {}, "rm": {}, "mkdir": {}, "rmdir": {}, "ln": {},
	"chmod": {}, "chown": {}, "chgrp": {},
	"touch": {}, "head": {}, "tail": {}, "wc": {}, "sort": {}, "uniq": {}, "cut": {},
	"tr": {}, "tee": {}, "xargs": {},
	"find": {}, "grep": {}, "egrep": {}, "fgrep": {}, "sed": {}, "awk": {},
	"diff": {}, "patch": {},
	"echo": {}, "printf": {}, "test": {}, "true": {}, "false": {}, "yes": {},
	"env": {}, "printenv": {},
	"pwd": {}, "cd": {}, "dirname": {}, "basename": {}, "realpath": {}, "readlink": {},
	"date": {}, "cal": {}, "sleep": {}, "kill": {}, "ps": {}, "top": {}, "df": {}, "du": {}, "free": {},
	"id": {}, "whoami": {}, "groups": {}, "su": {}, "sudo": {}, "passwd": {},
	"tar": {}, "gzip": {}, "gunzip": {}, "bzip2": {}, "xz": {}, "zip": {}, "unzip": {},
	"curl": {}, "wget": {},
	"mount": {}, "umount": {}, "fdisk": {}, "mkfs": {}, "fsck": {},
	"ifconfig": {}, "ip": {}, "ping": {}, "netstat": {}, "ss": {}, "hostname": {},
	"nslookup": {}, "dig": {},
	"ssh": {}, "scp": {}, "sftp": {},
	"vi": {}, "vim": {}, "nano": {}, "less": {}, "more": {},
	"uname": {}, "arch": {}, "nproc": {}, "lscpu": {}, "lsblk": {}, "lsof": {},
	"strace": {}, "ltrace": {}, "gdb": {},
	"apt": {}, "apt-get": {}, "apt-cache": {}, "dpkg": {}, "apk": {}, "yum": {},
	"dnf": {}, "rpm": {},
	"adduser": {}, "useradd": {}, "usermod": {}, "userdel": {}, "groupadd": {},
	"systemctl": {}, "service": {}, "init": {}, "reboot": {}, "shutdown": {}, "halt": {},
	"crontab": {}, "at": {},
	"dd": {}, "sync": {}, "dmesg": {}, "modprobe": {}, "insmod": {}, "lsmod": {},
	"chroot": {}, "nohup": {}, "nice": {}, "ionice": {}, "timeout": {},
	"seq": {}, "expr": {}, "bc": {}, "md5sum": {}, "sha256sum": {}, "sha1sum": {},
	"file": {}, "stat": {}, "strings": {}, "od": {}, "hexdump": {},
	"which": {}, "whereis": {}, "type": {}, "command": {},
	"clear": {}, "reset": {}, "tput": {}, "stty": {},
	"mktemp": {}, "install": {}, "tac": {}, "rev": {}, "fold": {}, "fmt": {}, "nl": {},
	"expand": {}, "unexpand": {},
	"vminitd": {}, "vmexec": {},
}
