// SPDX-License-Identifier: Apache-2.0

package prompter

import (
	"bytes"
	"strings"
	"testing"
)

func TestAskDefault(t *testing.T) {
	p := NewTerminalWith(strings.NewReader("\n"), &bytes.Buffer{})
	got, err := p.Ask("name?", "alice")
	if err != nil || got != "alice" {
		t.Fatalf("got %q err=%v", got, err)
	}
}

func TestAskNonEmpty(t *testing.T) {
	p := NewTerminalWith(strings.NewReader("bob\n"), &bytes.Buffer{})
	got, _ := p.Ask("name?", "alice")
	if got != "bob" {
		t.Fatalf("got %q", got)
	}
}

func TestAskYesNoDefaultYes(t *testing.T) {
	p := NewTerminalWith(strings.NewReader("\n"), &bytes.Buffer{})
	got, _ := p.AskYesNo("ok?", true)
	if !got {
		t.Fatal("expected true")
	}
}

func TestAskYesNoExplicitNo(t *testing.T) {
	p := NewTerminalWith(strings.NewReader("n\n"), &bytes.Buffer{})
	got, _ := p.AskYesNo("ok?", true)
	if got {
		t.Fatal("expected false")
	}
}

func TestPickValid(t *testing.T) {
	p := NewTerminalWith(strings.NewReader("2\n"), &bytes.Buffer{})
	got, err := p.Pick("which?", []string{"a", "b", "c"}, 0)
	if err != nil || got != 1 {
		t.Fatalf("got %d err=%v", got, err)
	}
}

func TestPickDefault(t *testing.T) {
	p := NewTerminalWith(strings.NewReader("\n"), &bytes.Buffer{})
	got, _ := p.Pick("which?", []string{"a", "b"}, 1)
	if got != 1 {
		t.Fatalf("got %d", got)
	}
}

func TestPickOutOfRange(t *testing.T) {
	p := NewTerminalWith(strings.NewReader("99\n"), &bytes.Buffer{})
	if _, err := p.Pick("which?", []string{"a", "b"}, 0); err != ErrInvalidChoice {
		t.Fatalf("want ErrInvalidChoice got %v", err)
	}
}

func TestConfirm(t *testing.T) {
	p := NewTerminalWith(strings.NewReader("YES\n"), &bytes.Buffer{})
	ok, _ := p.Confirm("type YES", "yes")
	if !ok {
		t.Fatal("expected true (case-insensitive)")
	}
	p = NewTerminalWith(strings.NewReader("no\n"), &bytes.Buffer{})
	ok, _ = p.Confirm("type YES", "yes")
	if ok {
		t.Fatal("expected false")
	}
}

func TestScripted(t *testing.T) {
	s := NewScripted("bob", "y", "2", "yes")
	if v, _ := s.Ask("name?", "alice"); v != "bob" {
		t.Fatalf("ask %q", v)
	}
	if v, _ := s.AskYesNo("ok?", false); !v {
		t.Fatal("yesno")
	}
	if v, _ := s.Pick("which?", []string{"a", "b", "c"}, 0); v != 1 {
		t.Fatalf("pick %d", v)
	}
	if v, _ := s.Confirm("confirm", "yes"); !v {
		t.Fatal("confirm")
	}
	// Exhausted queue returns an error.
	if _, err := s.Ask("x", ""); err == nil {
		t.Fatal("expected error on exhausted scripted prompter")
	}
}

func TestScriptedEmptyUsesDefault(t *testing.T) {
	s := NewScripted("")
	if v, _ := s.Ask("name?", "fallback"); v != "fallback" {
		t.Fatalf("got %q", v)
	}
}
