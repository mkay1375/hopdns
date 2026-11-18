package sniproxy

import (
	"io"
	"net"
	"runtime"

	"github.com/rcrowley/go-metrics"
)

const relayCopyChunk = 64 << 10

type relayBody struct {
	io.ReadCloser
	counter metrics.Counter
}

func (body relayBody) Read(p []byte) (int, error) {
	n, err := body.ReadCloser.Read(p)
	addRelayBytes(body.counter, int64(n))
	return n, err
}

type relayWriter struct {
	io.Writer
	counter metrics.Counter
}

func (writer relayWriter) Write(p []byte) (int, error) {
	n, err := writer.Writer.Write(p)
	addRelayBytes(writer.counter, int64(n))
	return n, err
}

func addRelayBytes(counter metrics.Counter, n int64) {
	if counter != nil && n > 0 {
		counter.Inc(n)
	}
}

func copyRelayBody(dst io.Writer, src io.Reader, counter metrics.Counter) error {
	if counter != nil {
		dst = relayWriter{Writer: dst, counter: counter}
	}
	_, err := io.Copy(dst, src)
	return err
}

func copyRelayStream(dst, src net.Conn, counter metrics.Counter) error {
	if runtime.GOOS == "linux" && counter != nil {
		dstTCP, directDestination := dst.(*net.TCPConn)
		_, directSource := src.(*net.TCPConn)
		if directDestination && directSource {
			// Preserve TCPConn.ReadFrom's splice fast path with a LimitedReader.
			// Each completed window becomes visible while the stream is open.
			limited := &io.LimitedReader{R: src}
			for {
				limited.N = relayCopyChunk
				n, err := io.Copy(dstTCP, limited)
				addRelayBytes(counter, n)
				if err != nil || limited.N > 0 {
					return err
				}
			}
		}
	}
	return copyRelayBody(dst, src, counter)
}
