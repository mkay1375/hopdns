package sniproxy

import (
	"io"
	"net"
	"testing"
	"time"
)

func hopDNSTCPPair(t *testing.T) (*net.TCPConn, *net.TCPConn) {
	t.Helper()
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	client, err := net.DialTCP("tcp4", nil, listener.Addr().(*net.TCPAddr))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	server, err := listener.AcceptTCP()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	deadline := time.Now().Add(3 * time.Second)
	if err := client.SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	if err := server.SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	return client, server
}

func TestHopDNSHalfClose(t *testing.T) {
	for _, originFirst := range []bool{false, true} {
		name := "upload FIN still allows response"
		if originFirst {
			name = "origin FIN still allows upload"
		}
		t.Run(name, func(t *testing.T) {
			client, proxyClient := hopDNSTCPPair(t)
			proxyOrigin, origin := hopDNSTCPPair(t)
			errors := make(chan error, 2)
			go proxyCopy(errors, proxyOrigin, proxyClient, nil)
			go proxyCopy(errors, proxyClient, proxyOrigin, nil)

			sendAndFinish := func(source *net.TCPConn, destination *net.TCPConn, data string) {
				t.Helper()
				if _, err := io.WriteString(source, data); err != nil {
					t.Fatal(err)
				}
				if err := source.CloseWrite(); err != nil {
					t.Fatal(err)
				}
				received, err := io.ReadAll(destination)
				if err != nil {
					t.Fatalf("expected propagated FIN, got %q and %v", received, err)
				}
				if string(received) != data {
					t.Fatalf("received %q, want %q", received, data)
				}
			}
			if originFirst {
				sendAndFinish(origin, client, "response without TLS close_notify")
				sendAndFinish(client, origin, "upload after response FIN")
			} else {
				sendAndFinish(client, origin, "upload body")
				sendAndFinish(origin, client, "response after upload FIN")
			}
			for i := 0; i < 2; i++ {
				select {
				case err := <-errors:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("proxy copy did not finish after both FINs")
				}
			}
		})
	}
}
