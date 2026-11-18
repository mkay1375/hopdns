package sniproxy

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rcrowley/go-metrics"
)

func TestHopDNSRelayStreamBytes(t *testing.T) {
	client, proxyClient := hopDNSTCPPair(t)
	proxyOrigin, origin := hopDNSTCPPair(t)
	count := metrics.NewCounter()
	finished := make(chan error, 1)
	go proxyCopy(finished, proxyOrigin, proxyClient, count)

	data := bytes.Repeat([]byte{'a'}, 2*relayCopyChunk+13)
	written := make(chan error, 1)
	go func() {
		_, err := client.Write(data)
		written <- err
	}()
	got := make([]byte, len(data))
	if _, err := io.ReadFull(origin, got); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("could not copy test stream: %v", err)
	}
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for count.Count() < 2*relayCopyChunk && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if n := count.Count(); n < 2*relayCopyChunk {
		t.Fatalf("in-progress transfer count = %d, expected at least %d", n, 2*relayCopyChunk)
	}
	if err := client.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if _, err := origin.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("expected a propagated FIN, got %v", err)
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if got := count.Count(); got != int64(len(data)) {
		t.Fatalf("final transfer count = %d, expected %d", got, len(data))
	}
}

func TestHopDNSRelayHTTPBodyBytes(t *testing.T) {
	const upload = "client body"
	const download = "origin response body"
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if string(data) != upload || err != nil {
			t.Errorf("body = %q, error = %v", data, err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, download)
	}))
	defer origin.Close()
	transport := &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, origin.Listener.Addr().String())
	}}
	defer transport.CloseIdleConnections()
	c := newTestConfig()
	c.RelayBytesToClient = metrics.NewCounter()
	c.RelayBytesToOrigin = metrics.NewCounter()
	handler := handle80(c, testLogger(), transport)
	accepted := httptest.NewRecorder()
	handler.ServeHTTP(accepted, httptest.NewRequest(http.MethodPost, "http://relay.example/upload", strings.NewReader(upload)))
	if accepted.Code != http.StatusOK || accepted.Body.String() != download {
		t.Fatalf("response = %d %q", accepted.Code, accepted.Body.String())
	}
	refused := httptest.NewRecorder()
	handler.ServeHTTP(refused, httptest.NewRequest(http.MethodPost, "http://127.0.0.1/refused", strings.NewReader(upload)))
	if refused.Code != http.StatusNotFound {
		t.Fatalf("expected local target to be refused, got %d", refused.Code)
	}
	if n := c.RelayBytesToOrigin.Count(); n != int64(len(upload)) {
		t.Fatalf("client to origin = %d, expected %d", n, len(upload))
	}
	if n := c.RelayBytesToClient.Count(); n != int64(len(download)) {
		t.Fatalf("origin to client = %d, expected %d", n, len(download))
	}
}
