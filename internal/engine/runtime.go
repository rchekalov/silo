// SPDX-License-Identifier: Apache-2.0

// Package engine orchestrates container VM lifecycles. The runtime bootstrap
// lives here because it conceptually belongs with the rest of the engine
// (kernel and initfs are prerequisites for every VM we boot).
package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/rchekalov/silo/internal/errs"
	"github.com/rchekalov/silo/internal/runtime"
	"github.com/rchekalov/silo/internal/version"
)

const (
	kataVersion         = "3.17.0"
	kernelPathInTarball = "opt/kata/share/kata-containers/vmlinux.container"
	vminitdSwiftVersion = "6.3.0"
	vminitdSDKURL       = "https://download.swift.org/swift-6.3-release/static-sdk/swift-6.3-RELEASE/swift-6.3-RELEASE_static-linux-0.1.0.artifactbundle.tar.gz"

	// containerizationRef pins the apple/containerization tag used by the
	// source-build fallback. Keep aligned with swift-bridge/Package.resolved so
	// dev builds and the source fallback resolve the same revision.
	containerizationRef    = "0.30.1"
	containerizationGitURL = "https://github.com/apple/containerization.git"

	// runtimeBundleAsset names the asset attached to GitHub releases by the
	// release workflow. The client tries to download this before falling back
	// to building the runtime from source.
	runtimeBundleAsset    = "silo-runtime-arm64.tar.gz"
	runtimeBundleChecksum = runtimeBundleAsset + ".sha256"
)

// runtimeBundleBaseURL is overridable in tests. Production URL is derived from
// the baked-in silo version.
var runtimeBundleBaseURL = func() string {
	return fmt.Sprintf("https://github.com/rchekalov/silo/releases/download/v%s", version.Version)
}

// RuntimeReady reports whether vmlinux + initfs.ext4 are both installed.
func RuntimeReady() bool {
	_, kernErr := os.Stat(runtime.Kernel())
	_, iniErr := os.Stat(runtime.Initfs())
	return kernErr == nil && iniErr == nil
}

// EnsureRuntime fetches or builds every prerequisite. On the happy path, the
// client downloads a prebuilt runtime bundle from the matching GitHub release
// (~30 s). If that's unavailable — offline, dev build, pre-release tag without
// a bundle — it falls back to downloading the kernel and building vminitd from
// source (~5 min). Safe to call repeatedly; idempotent once ~/.silo/vmlinux and
// ~/.silo/initfs.ext4 exist.
func EnsureRuntime() error {
	if err := runtime.EnsureDirectories(); err != nil {
		return err
	}
	if RuntimeReady() {
		return nil
	}

	if ok, err := tryDownloadRuntimeBundle(); err != nil {
		// Hard error (e.g., the bundle was found but extraction failed in a
		// way that corrupts the install). Surface it instead of masking.
		return err
	} else if ok {
		return nil
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

	containerizationDir, err := ensureContainerizationCheckout()
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
			// Preserve exec bit across copyBytes so cctl packages the binaries
			// into initfs with the right mode — otherwise the guest init fails
			// with EACCES at boot.
			if err := os.Chmod(pair[1], 0o755); err != nil {
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
		// copyBytes uses os.Create, which drops the source's exec bit — restore
		// it so we can actually run cctl below. Hit on hosted CI runners.
		if err := os.Chmod(cctlBin, 0o755); err != nil {
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

// ensureContainerizationCheckout returns a directory holding the
// apple/containerization source tree. It prefers an existing SwiftPM-resolved
// checkout under the current working directory (so `make build` keeps working
// without a second clone), and otherwise clones the pinned tag into
// ~/.silo/.local/containerization.
//
// This is what makes the build-from-source fallback work for users who
// installed silo via Homebrew (and therefore have no project checkout).
func ensureContainerizationCheckout() (string, error) {
	if dir, ok := existingContainerizationCheckout(); ok {
		return dir, nil
	}

	dest := filepath.Join(runtime.LocalDownloads(), "containerization")
	if _, err := os.Stat(filepath.Join(dest, "vminitd", "Package.swift")); err == nil {
		return dest, nil
	}

	if err := os.MkdirAll(runtime.LocalDownloads(), 0o755); err != nil {
		return "", err
	}
	// Remove partial clones from previous interrupted runs.
	_ = os.RemoveAll(dest)

	fmt.Fprintf(os.Stderr, "Cloning apple/containerization@%s...\n", containerizationRef)
	if err := runCmd("/usr/bin/git",
		"clone", "--depth=1",
		"--branch", containerizationRef,
		containerizationGitURL,
		dest,
	); err != nil {
		return "", errs.Runtimef(
			"could not fetch apple/containerization@%s: %v (check your network, or install silo from a source checkout)",
			containerizationRef, err,
		)
	}
	if _, err := os.Stat(filepath.Join(dest, "vminitd", "Package.swift")); err != nil {
		return "", errs.Runtimef(
			"cloned apple/containerization but vminitd/Package.swift is missing — unexpected repo layout at ref %s",
			containerizationRef,
		)
	}
	return dest, nil
}

// existingContainerizationCheckout probes the two SwiftPM locations a source
// build of silo would populate. Only used as a fast-path for developers
// working inside the silo repo.
func existingContainerizationCheckout() (string, bool) {
	cwd, _ := os.Getwd()
	candidates := []string{
		filepath.Join(cwd, ".build", "checkouts", "containerization"),
		filepath.Join(cwd, "swift-bridge", ".build", "checkouts", "containerization"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(filepath.Join(c, "vminitd", "Package.swift")); err == nil {
			return c, true
		}
	}
	return "", false
}

// tryDownloadRuntimeBundle fetches silo-runtime-arm64.tar.gz + its .sha256
// from the GitHub release matching the current silo version, verifies the
// checksum, and extracts vmlinux + initfs.ext4 into ~/.silo/. Returns
// (true, nil) on success. On any transient failure (404, network error,
// checksum mismatch), it logs a one-line note and returns (false, nil) so the
// caller can fall back to building from source. Only hard extraction failures
// that would leave ~/.silo/ corrupted bubble up as errors.
func tryDownloadRuntimeBundle() (bool, error) {
	base := runtimeBundleBaseURL()
	bundleURL := base + "/" + runtimeBundleAsset
	sumURL := base + "/" + runtimeBundleChecksum

	local := runtime.LocalDownloads()
	if err := os.MkdirAll(local, 0o755); err != nil {
		return false, err
	}
	bundlePath := filepath.Join(local, runtimeBundleAsset)
	sumPath := filepath.Join(local, runtimeBundleChecksum)

	// Best-effort cleanup of any partial download from a prior run.
	_ = os.Remove(bundlePath)
	_ = os.Remove(sumPath)

	fmt.Fprintln(os.Stderr, "Downloading prebuilt silo runtime...")
	if err := httpDownload(sumURL, sumPath); err != nil {
		fmt.Fprintf(os.Stderr, "Prebuilt runtime unavailable (%v); building from source instead.\n", err)
		return false, nil
	}
	want, err := readExpectedSha256(sumPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Prebuilt runtime checksum unreadable (%v); building from source instead.\n", err)
		return false, nil
	}
	if err := httpDownload(bundleURL, bundlePath); err != nil {
		fmt.Fprintf(os.Stderr, "Prebuilt runtime download failed (%v); building from source instead.\n", err)
		return false, nil
	}

	got, err := sha256File(bundlePath)
	if err != nil {
		return false, err
	}
	if got != want {
		fmt.Fprintf(os.Stderr,
			"Prebuilt runtime checksum mismatch (want %s, got %s); building from source instead.\n",
			want, got,
		)
		return false, nil
	}

	// Extract into a staging dir first so a partial extract doesn't pollute
	// ~/.silo/ with half-written files that RuntimeReady() would then lie about.
	stage := filepath.Join(local, "runtime-stage")
	_ = os.RemoveAll(stage)
	if err := os.MkdirAll(stage, 0o755); err != nil {
		return false, err
	}
	if err := runCmd("/usr/bin/tar", "-xzf", bundlePath, "-C", stage); err != nil {
		return false, errs.Runtimef("extract runtime bundle: %v", err)
	}
	for _, name := range []string{"vmlinux", "initfs.ext4"} {
		src := filepath.Join(stage, name)
		if _, err := os.Stat(src); err != nil {
			return false, errs.Runtimef("runtime bundle missing %s", name)
		}
	}
	for _, pair := range [][2]string{
		{"vmlinux", runtime.Kernel()},
		{"initfs.ext4", runtime.Initfs()},
	} {
		if err := copyBytes(filepath.Join(stage, pair[0]), pair[1]); err != nil {
			return false, errs.Runtimef("install %s: %v", pair[0], err)
		}
	}
	if err := os.Chmod(runtime.Kernel(), 0o755); err != nil {
		return false, err
	}
	_ = os.RemoveAll(stage)
	_ = os.Remove(bundlePath)
	_ = os.Remove(sumPath)

	fmt.Fprintln(os.Stderr, "Runtime installed from prebuilt bundle.")
	return true, nil
}

// httpDownload fetches url to dest, returning an error with the HTTP status
// when the server doesn't serve the asset (e.g., no prebuilt for this tag).
func httpDownload(url, dest string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %s", resp.Status)
	}
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, resp.Body); err != nil {
		return err
	}
	return out.Sync()
}

// readExpectedSha256 parses `shasum -a 256` format: "<hex>  <filename>".
// Either the lone hex string or the full line is accepted.
func readExpectedSha256(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(raw))
	if len(fields) == 0 {
		return "", fmt.Errorf("empty checksum file")
	}
	s := strings.ToLower(fields[0])
	if len(s) != 64 {
		return "", fmt.Errorf("not a sha256 hex string: %q", fields[0])
	}
	return s, nil
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
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
