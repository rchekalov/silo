// SPDX-License-Identifier: Apache-2.0

package cache

import (
	"errors"
	"io"
	"os"

	"github.com/klauspost/compress/zstd"
)

// CompressedSuffix is the on-disk suffix for zstd-compressed rootfs ext4
// files. Keep in sync with List/Entries/Path parsing.
const CompressedSuffix = ".ext4.zst"

// compressExt4 writes a zstd-compressed copy of `src` to `dst`. Uses a
// streaming encoder with the default level (≈zstd-3).
func compressExt4(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	tmp := dst + ".tmp-" + randomHex()
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	enc, err := zstd.NewWriter(out, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if _, err := io.Copy(enc, in); err != nil {
		_ = enc.Close()
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := enc.Close(); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// decompressExt4 writes a zstd-decompressed copy of `src` to `dst`. On
// success the destination holds the original raw ext4 bytes.
func decompressExt4(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	dec, err := zstd.NewReader(in)
	if err != nil {
		return err
	}
	defer dec.Close()

	tmp := dst + ".tmp-" + randomHex()
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, dec); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// errNotCompressed signals that Decompress was called for a digest that has
// no compressed entry.
var errNotCompressed = errors.New("cache: no compressed entry for digest")
