// SPDX-License-Identifier: Apache-2.0

// Package lsp implements JSON-RPC Content-Length framing, host<->guest path
// rewriting, and IDE config generation for Silo's LSP proxy.
package lsp

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// ErrMissingContentLength is returned when a message has no Content-Length header.
var ErrMissingContentLength = errors.New("lsp: missing Content-Length header")

// FrameReader reads length-prefixed JSON-RPC messages from an io.Reader.
type FrameReader struct {
	r *bufio.Reader
}

// NewFrameReader wraps r with buffering and a frame parser.
func NewFrameReader(r io.Reader) *FrameReader { return &FrameReader{r: bufio.NewReader(r)} }

// ReadMessage returns the next message body. Returns (nil, nil) on clean EOF.
func (f *FrameReader) ReadMessage() ([]byte, error) {
	length := -1
	for {
		line, err := f.r.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) && line == "" {
				return nil, nil
			}
			return nil, err
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			break
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(key), "content-length") {
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil, fmt.Errorf("lsp: parse Content-Length: %w", err)
			}
			length = n
		}
	}
	if length < 0 {
		return nil, ErrMissingContentLength
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(f.r, body); err != nil {
		return nil, err
	}
	return body, nil
}

// FrameWriter prefixes bodies with Content-Length headers.
type FrameWriter struct {
	w io.Writer
}

// NewFrameWriter returns a FrameWriter over w.
func NewFrameWriter(w io.Writer) *FrameWriter { return &FrameWriter{w: w} }

// WriteMessage writes the frame for body.
func (f *FrameWriter) WriteMessage(body []byte) error {
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	if _, err := f.w.Write([]byte(header)); err != nil {
		return err
	}
	if _, err := f.w.Write(body); err != nil {
		return err
	}
	if flusher, ok := f.w.(interface{ Flush() error }); ok {
		return flusher.Flush()
	}
	return nil
}
