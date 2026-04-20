// SPDX-License-Identifier: Apache-2.0

// Package engine orchestrates container VM lifecycles. The runtime bootstrap
// lives here because it conceptually belongs with the rest of the engine
// (kernel and initfs are prerequisites for every VM we boot).
package engine

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/rchekalov/silo/internal/errs"
	"github.com/rchekalov/silo/internal/runtime"
)

const (
	kataVersion           = "3.17.0"
	kernelPathInTarball   = "opt/kata/share/kata-containers/vmlinux.container"
	vminitdSwiftVersion   = "6.3.0"
	vminitdSDKURL         = "https://download.swift.org/swift-6.3-release/static-sdk/swift-6.3-RELEASE/swift-6.3-RELEASE_static-linux-0.1.0.artifactbundle.tar.gz"
)

// RuntimeReady reports whether vmlinux + initfs.ext4 are both installed.
func RuntimeReady() bool {
	_, kernErr := os.Stat(runtime.Kernel())
	_, iniErr := os.Stat(runtime.Initfs())
	return kernErr == nil && iniErr == nil
}

// EnsureRuntime fetches and builds every prerequisite (one-time, ~5 min on
// first run, cached at ~/.silo/ thereafter). Safe to call repeatedly.
func EnsureRuntime() error {
	if err := runtime.EnsureDirectories(); err != nil {
		return err
	}
	if err := ensureKernel(); err != nil {
		return err
	}
	return ensureInitfs()
}

func ensureKernel() error {
	if _, err := os.Stat(runtime.Kernel()); err == nil {
		return nil
	}

	localDir := runtime.LocalDownloads()
	tarball := filepath.Join(localDir, "kata.tar.xz")
	extracted := filepath.Join(localDir, "vmlinux")

	if err := os.MkdirAll(localDir, 0o755); err != nil {
		return err
	}

	if _, err := os.Stat(tarball); err != nil {
		url := fmt.Sprintf(
			"https://github.com/kata-containers/kata-containers/releases/download/%s/kata-static-%s-arm64.tar.xz",
			kataVersion, kataVersion,
		)
		fmt.Fprintf(os.Stderr, "Downloading Linux kernel from Kata Containers %s (~100 MB)...\n", kataVersion)
		if err := download(url, tarball); err != nil {
			return err
		}
	}

	if _, err := os.Stat(extracted); err != nil {
		fmt.Fprintln(os.Stderr, "Extracting kernel...")
		if err := runCmd("/usr/bin/tar", "-xf", tarball, "-C", localDir, "--strip-components=1"); err != nil {
			return err
		}
		nested := filepath.Join(localDir, kernelPathInTarball)
		if _, err := os.Stat(nested); err != nil {
			return errs.Runtimef("kernel missing at expected path in tarball: %s", kernelPathInTarball)
		}
		resolved, err := filepath.EvalSymlinks(nested)
		if err != nil {
			return err
		}
		if err := copyBytes(resolved, extracted); err != nil {
			return err
		}
	}

	if err := copyBytes(extracted, runtime.Kernel()); err != nil {
		return err
	}
	if err := os.Chmod(runtime.Kernel(), 0o755); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Kernel installed at %s\n", runtime.Kernel())
	return nil
}

func ensureInitfs() error {
	if _, err := os.Stat(runtime.Initfs()); err == nil {
		return nil
	}

	containerizationDir, err := findContainerizationCheckout()
	if err != nil {
		return err
	}
	vminitdDir := filepath.Join(containerizationDir, "vminitd")

	swiftBin, err := ensureSwiftlyToolchain()
	if err != nil {
		return err
	}

	vminitdBin := filepath.Join(vminitdDir, "bin", "vminitd")
	vmexecBin := filepath.Join(vminitdDir, "bin", "vmexec")

	if _, err := os.Stat(vminitdBin); err != nil {
		fmt.Fprintln(os.Stderr, "Building vminitd (cross-compiling for Linux)...")
		if err := runCmd(swiftBin,
			"build", "-c", "release",
			"--swift-sdk", "aarch64-swift-linux-musl",
			"-Xlinker", "-s",
			"--package-path", vminitdDir,
		); err != nil {
			return err
		}
		showBin, err := runCapture(swiftBin,
			"build", "-c", "release",
			"--swift-sdk", "aarch64-swift-linux-musl",
			"--package-path", vminitdDir,
			"--show-bin-path",
		)
		if err != nil {
			return err
		}
		buildBinDir := strings.TrimSpace(showBin)
		if err := os.MkdirAll(filepath.Join(vminitdDir, "bin"), 0o755); err != nil {
			return err
		}
		for _, pair := range [][2]string{
			{"vminitd", vminitdBin},
			{"vmexec", vmexecBin},
		} {
			src := filepath.Join(buildBinDir, pair[0])
			_ = os.Remove(pair[1])
			if err := copyBytes(src, pair[1]); err != nil {
				return err
			}
		}
		fmt.Fprintln(os.Stderr, "vminitd built successfully.")
	}

	cctlBin := filepath.Join(containerizationDir, "bin", "cctl")
	if _, err := os.Stat(cctlBin); err != nil {
		fmt.Fprintln(os.Stderr, "Building cctl...")
		if err := runCmd("/usr/bin/swift",
			"build", "-c", "release", "--product", "cctl",
			"--package-path", containerizationDir,
		); err != nil {
			return err
		}
		showBin, err := runCapture("/usr/bin/swift",
			"build", "-c", "release", "--product", "cctl",
			"--package-path", containerizationDir,
			"--show-bin-path",
		)
		if err != nil {
			return err
		}
		buildBinDir := strings.TrimSpace(showBin)
		if err := os.MkdirAll(filepath.Join(containerizationDir, "bin"), 0o755); err != nil {
			return err
		}
		built := filepath.Join(buildBinDir, "cctl")
		_ = os.Remove(cctlBin)
		if err := copyBytes(built, cctlBin); err != nil {
			return err
		}
		entitlements := filepath.Join(containerizationDir, "signing", "vz.entitlements")
		if err := runCmd("/usr/bin/codesign",
			"--force", "--sign", "-", "--timestamp=none",
			"--entitlements="+entitlements,
			cctlBin,
		); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "cctl built successfully.")
	}

	rootfsTar := filepath.Join(runtime.Root(), "init.rootfs.tar.gz")
	fmt.Fprintln(os.Stderr, "Creating initfs ext4 image...")
	if err := runCmd(cctlBin,
		"rootfs", "create",
		"--vminitd", vminitdBin,
		"--vmexec", vmexecBin,
		"--ext4", runtime.Initfs(),
		rootfsTar,
	); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Initfs installed at %s\n", runtime.Initfs())
	return nil
}

func ensureSwiftlyToolchain() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	binDir := filepath.Join(home, ".swiftly", "bin")
	swiftlyBin := filepath.Join(binDir, "swiftly")
	swiftBin := filepath.Join(binDir, "swift")

	if _, err := os.Stat(swiftlyBin); err != nil {
		fmt.Fprintln(os.Stderr, "Installing swiftly (Swift version manager, ~20 MB)...")
		pkgPath := "/tmp/swiftly.pkg"
		if _, err := os.Stat(pkgPath); err != nil {
			if err := download("https://download.swift.org/swiftly/darwin/swiftly.pkg", pkgPath); err != nil {
				return "", err
			}
		}
		if err := runCmd("/usr/sbin/installer",
			"-pkg", pkgPath,
			"-target", "CurrentUserHomeDirectory",
		); err != nil {
			return "", err
		}
		if err := runCmd(swiftlyBin, "init", "--quiet-shell-followup", "--skip-install"); err != nil {
			return "", err
		}
	}

	fmt.Fprintf(os.Stderr, "Ensuring Swift %s is installed (via swiftly)...\n", vminitdSwiftVersion)
	if err := runCmd(swiftlyBin, "install", vminitdSwiftVersion); err != nil {
		return "", err
	}

	sdkList, err := runCapture(swiftBin, "sdk", "list")
	if err != nil {
		return "", err
	}
	if !strings.Contains(sdkList, "static-linux") {
		fmt.Fprintln(os.Stderr, "Installing Static Linux SDK (~500 MB)...")
		sdkPath := "/tmp/swift-static-linux-sdk.tar.gz"
		if _, err := os.Stat(sdkPath); err != nil {
			if err := download(vminitdSDKURL, sdkPath); err != nil {
				return "", err
			}
		}
		if err := runCmd(swiftBin, "sdk", "install", sdkPath); err != nil {
			return "", err
		}
		fmt.Fprintln(os.Stderr, "Static Linux SDK installed.")
	}
	return swiftBin, nil
}

func findContainerizationCheckout() (string, error) {
	cwd, _ := os.Getwd()
	candidates := []string{
		filepath.Join(cwd, ".build", "checkouts", "containerization"),
		filepath.Join(cwd, "swift-bridge", ".build", "checkouts", "containerization"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(filepath.Join(c, "vminitd", "Package.swift")); err == nil {
			return c, nil
		}
	}
	return "", errs.Runtimef(
		"cannot find containerization source checkout. Run from the silo project directory, " +
			"or build and install vminitd manually. See: https://github.com/apple/containerization",
	)
}

func runCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return errs.Runtimef("%s failed: %v", filepath.Base(name), err)
	}
	return nil
}

func runCapture(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func download(url, dest string) error {
	return runCmd("/usr/bin/curl", "-fL#", "-o", dest, url)
}

func copyBytes(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
