// SPDX-License-Identifier: Apache-2.0

// Package prompter abstracts interactive user input so commands can be tested
// with scripted answers. The Rust/Go commands used ad-hoc bufio reads against
// os.Stdin which made them hard to drive from tests — this package replaces
// that pattern with a single injectable interface.
//
// Inspired by Sources/SiloConfig/IO/Prompter.swift on the main branch.
package prompter

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
)

// Prompter asks the user a question and returns their answer.
// All methods return an error if the underlying reader fails (e.g. stdin closed).
type Prompter interface {
	// Ask reads free-form text with an optional default. Empty input returns defaultValue.
	Ask(question, defaultValue string) (string, error)
	// AskYesNo returns true for "y"/"yes" (case-insensitive). Empty input returns defaultYes.
	AskYesNo(question string, defaultYes bool) (bool, error)
	// Pick prints choices as a numbered list and returns the selected index (0-based).
	// Empty input returns defaultIndex; out-of-range returns ErrInvalidChoice.
	Pick(question string, choices []string, defaultIndex int) (int, error)
	// Confirm requires the user to type requiredExact verbatim (case-insensitive).
	Confirm(question, requiredExact string) (bool, error)
}

// ErrInvalidChoice indicates Pick got an input that wasn't a valid index.
var ErrInvalidChoice = errors.New("prompter: invalid choice")

// Terminal is the default Prompter — prints to stderr, reads from stdin.
// Prompts go to stderr so they don't contaminate stdout when a CLI is being piped.
type Terminal struct {
	in  io.Reader
	out io.Writer
}

// NewTerminal returns a Terminal wrapping os.Stdin / os.Stderr.
func NewTerminal() *Terminal {
	return &Terminal{in: os.Stdin, out: os.Stderr}
}

// NewTerminalWith returns a Terminal over custom streams (tests).
func NewTerminalWith(in io.Reader, out io.Writer) *Terminal {
	return &Terminal{in: in, out: out}
}

func (t *Terminal) Ask(question, defaultValue string) (string, error) {
	if defaultValue != "" {
		fmt.Fprintf(t.out, "%s [%s] ", question, defaultValue)
	} else {
		fmt.Fprintf(t.out, "%s ", question)
	}
	line, err := readLine(t.in)
	if err != nil {
		return "", err
	}
	if line == "" {
		return defaultValue, nil
	}
	return line, nil
}

func (t *Terminal) AskYesNo(question string, defaultYes bool) (bool, error) {
	suffix := "[y/N]"
	if defaultYes {
		suffix = "[Y/n]"
	}
	fmt.Fprintf(t.out, "%s %s ", question, suffix)
	line, err := readLine(t.in)
	if err != nil {
		return false, err
	}
	if line == "" {
		return defaultYes, nil
	}
	lower := strings.ToLower(line)
	return lower == "y" || lower == "yes", nil
}

func (t *Terminal) Pick(question string, choices []string, defaultIndex int) (int, error) {
	for i, c := range choices {
		fmt.Fprintf(t.out, "  [%d] %s\n", i+1, c)
	}
	fmt.Fprintf(t.out, "%s (default: %d): ", question, defaultIndex+1)
	line, err := readLine(t.in)
	if err != nil {
		return -1, err
	}
	if line == "" {
		return defaultIndex, nil
	}
	n, err := strconv.Atoi(line)
	if err != nil || n < 1 || n > len(choices) {
		return -1, ErrInvalidChoice
	}
	return n - 1, nil
}

func (t *Terminal) Confirm(question, requiredExact string) (bool, error) {
	fmt.Fprintln(t.out, question)
	line, err := readLine(t.in)
	if err != nil {
		return false, err
	}
	return strings.EqualFold(strings.TrimSpace(line), requiredExact), nil
}

// readLine reads one line from r, trimming trailing whitespace.
func readLine(r io.Reader) (string, error) {
	br, ok := r.(*bufio.Reader)
	if !ok {
		br = bufio.NewReader(r)
	}
	line, err := br.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	if line == "" && err == io.EOF {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// Scripted is a test double that returns preloaded answers in order.
// Each call to Ask/AskYesNo/Pick/Confirm consumes one answer from the queue.
type Scripted struct {
	mu      sync.Mutex
	answers []string
}

// NewScripted preloads answers. Pass them in the order the code under test
// will consume them.
func NewScripted(answers ...string) *Scripted {
	return &Scripted{answers: append([]string(nil), answers...)}
}

func (s *Scripted) next() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.answers) == 0 {
		return "", errors.New("prompter: no scripted answers left")
	}
	a := s.answers[0]
	s.answers = s.answers[1:]
	return a, nil
}

func (s *Scripted) Ask(_, defaultValue string) (string, error) {
	a, err := s.next()
	if err != nil {
		return "", err
	}
	if a == "" {
		return defaultValue, nil
	}
	return a, nil
}

func (s *Scripted) AskYesNo(_ string, defaultYes bool) (bool, error) {
	a, err := s.next()
	if err != nil {
		return false, err
	}
	if a == "" {
		return defaultYes, nil
	}
	lower := strings.ToLower(a)
	return lower == "y" || lower == "yes", nil
}

func (s *Scripted) Pick(_ string, choices []string, defaultIndex int) (int, error) {
	a, err := s.next()
	if err != nil {
		return -1, err
	}
	if a == "" {
		return defaultIndex, nil
	}
	n, err := strconv.Atoi(a)
	if err != nil || n < 1 || n > len(choices) {
		return -1, ErrInvalidChoice
	}
	return n - 1, nil
}

func (s *Scripted) Confirm(_, requiredExact string) (bool, error) {
	a, err := s.next()
	if err != nil {
		return false, err
	}
	return strings.EqualFold(strings.TrimSpace(a), requiredExact), nil
}
