// SPDX-License-Identifier: Apache-2.0

package network

import (
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rchekalov/silo/internal/config"
)

// startEchoServer listens on 127.0.0.1:0 and echoes bytes back until close.
func startEchoServer(t *testing.T) (uint16, func()) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := uint16(l.Addr().(*net.TCPAddr).Port)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 1024)
				for {
					n, err := c.Read(buf)
					if n > 0 {
						_, _ = c.Write(buf[:n])
					}
					if err != nil {
						return
					}
				}
			}(c)
		}
	}()
	return port, func() {
		_ = l.Close()
		<-done
	}
}

func freePort(t *testing.T) uint16 {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := uint16(l.Addr().(*net.TCPAddr).Port)
	_ = l.Close()
	return port
}

func TestBasicForwarding(t *testing.T) {
	echoPort, stopEcho := startEchoServer(t)
	defer stopEcho()
	hostPort := freePort(t)

	pf, err := StartPortForwarder([]config.PortMapping{{Host: hostPort, Guest: echoPort}}, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	defer pf.Stop()

	c, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", itoaPort(hostPort)))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "hello" {
		t.Fatalf("got %q", buf)
	}
}

func TestPortAlreadyBound(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := uint16(l.Addr().(*net.TCPAddr).Port)
	defer l.Close()

	_, err = StartPortForwarder([]config.PortMapping{{Host: port, Guest: 9999}}, "127.0.0.1")
	if err == nil {
		t.Fatal("expected bind error")
	}
	if !strings.Contains(err.Error(), "address already in use") && !errors.Is(err, net.ErrClosed) {
		t.Logf("got error: %v", err)
	}
}

func TestStopAbortsAccept(t *testing.T) {
	echoPort, stopEcho := startEchoServer(t)
	defer stopEcho()
	hostPort := freePort(t)

	pf, err := StartPortForwarder([]config.PortMapping{{Host: hostPort, Guest: echoPort}}, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}

	// Sanity: works.
	c, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", itoaPort(hostPort)))
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()

	pf.Stop()
	time.Sleep(50 * time.Millisecond)
	if _, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", itoaPort(hostPort))); err == nil {
		t.Fatal("expected connect failure after Stop")
	}
}

func itoaPort(p uint16) string {
	return strconv.Itoa(int(p))
}
