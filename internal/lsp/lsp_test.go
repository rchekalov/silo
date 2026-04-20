// SPDX-License-Identifier: Apache-2.0

package lsp

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestFrameReaderSingleMessage(t *testing.T) {
	r := NewFrameReader(strings.NewReader("Content-Length: 15\r\n\r\n{\"id\":1,\"ok\":1}"))
	msg, err := r.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if string(msg) != `{"id":1,"ok":1}` {
		t.Fatalf("got %q", msg)
	}
}

func TestFrameReaderMultiple(t *testing.T) {
	r := NewFrameReader(strings.NewReader("Content-Length: 5\r\n\r\nhelloContent-Length: 5\r\n\r\nworld"))
	m1, _ := r.ReadMessage()
	m2, _ := r.ReadMessage()
	if string(m1) != "hello" || string(m2) != "world" {
		t.Fatalf("got %q / %q", m1, m2)
	}
}

func TestFrameReaderEOF(t *testing.T) {
	r := NewFrameReader(strings.NewReader(""))
	msg, err := r.ReadMessage()
	if err != nil || msg != nil {
		t.Fatalf("got msg=%v err=%v", msg, err)
	}
}

func TestFrameReaderCaseInsensitive(t *testing.T) {
	r := NewFrameReader(strings.NewReader("content-length: 2\r\n\r\nok"))
	msg, err := r.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if string(msg) != "ok" {
		t.Fatalf("got %q", msg)
	}
}

func TestFrameReaderExtraHeaders(t *testing.T) {
	r := NewFrameReader(strings.NewReader("Content-Type: application/json\r\nContent-Length: 2\r\n\r\nok"))
	msg, err := r.ReadMessage()
	if err != nil || string(msg) != "ok" {
		t.Fatalf("got %q err=%v", msg, err)
	}
}

func TestFrameWriter(t *testing.T) {
	var buf bytes.Buffer
	w := NewFrameWriter(&buf)
	if err := w.WriteMessage([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "Content-Length: 5\r\n\r\nhello" {
		t.Fatalf("got %q", buf.String())
	}
}

func TestFrameRoundtrip(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","id":1}`)
	var buf bytes.Buffer
	_ = NewFrameWriter(&buf).WriteMessage(body)
	msg, err := NewFrameReader(&buf).ReadMessage()
	if err != nil || !bytes.Equal(msg, body) {
		t.Fatalf("got %q err=%v", msg, err)
	}
}

func TestProxyRewriteRawPath(t *testing.T) {
	p := NewProxy("/Users/me/project", "/workspace")
	in := []byte(`{"uri":"/Users/me/project/src/main.py"}`)
	got := p.RewriteInbound(in)
	if string(got) != `{"uri":"/workspace/src/main.py"}` {
		t.Fatalf("got %s", got)
	}
}

func TestProxyRewriteOutbound(t *testing.T) {
	p := NewProxy("/Users/me/project", "/workspace")
	in := []byte(`{"uri":"/workspace/src/main.py"}`)
	got := p.RewriteOutbound(in)
	if string(got) != `{"uri":"/Users/me/project/src/main.py"}` {
		t.Fatalf("got %s", got)
	}
}

func TestProxyRewriteFileURI(t *testing.T) {
	p := NewProxy("/Users/me/project", "/workspace")
	in := []byte(`{"uri":"file:///Users/me/project/foo.py"}`)
	got := p.RewriteInbound(in)
	if string(got) != `{"uri":"file:///workspace/foo.py"}` {
		t.Fatalf("got %s", got)
	}
}

func TestProxyRoundtrip(t *testing.T) {
	p := NewProxy("/Users/me/project", "/workspace")
	orig := []byte(`{"uri":"file:///Users/me/project/foo.py"}`)
	guest := p.RewriteInbound(orig)
	back := p.RewriteOutbound(guest)
	if !bytes.Equal(back, orig) {
		t.Fatalf("got %s", back)
	}
}

func TestProxyRewriteWithSpaces(t *testing.T) {
	p := NewProxy("/Users/me/my project", "/workspace")
	in := []byte(`{"uri":"file:///Users/me/my%20project/foo.py"}`)
	got := p.RewriteInbound(in)
	if string(got) != `{"uri":"file:///workspace/foo.py"}` {
		t.Fatalf("got %s", got)
	}
}

func TestPercentEncode(t *testing.T) {
	if PercentEncodePath("/workspace") != "/workspace" {
		t.Fatal()
	}
	if PercentEncodePath("/Users/me/project") != "/Users/me/project" {
		t.Fatal()
	}
	if PercentEncodePath("/Users/me/my project") != "/Users/me/my%20project" {
		t.Fatal()
	}
}

// Ensure io package is used via test compile.
var _ = io.EOF
