//go:build cgo || with_purego

package lwip

import (
	"bytes"
	"errors"
	"io"
	"net"
	"os"
	"testing"
	"time"
)

func TestTCPReadBatch(t *testing.T) {
	s, c, _, server := established(t)
	data := make([]byte, 60000)
	for i := range data {
		data[i] = byte(i*31 + 7)
	}
	if err := s.Input(tcpPacket(101, server, 16, data)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ReadBatch([][]byte{make([]byte, 10), nil}, make([]int, 2)); !errors.Is(err, io.ErrShortBuffer) {
		t.Fatal("invalid batch consumed data", err)
	}
	buffers := [][]byte{make([]byte, 20000), make([]byte, 50000), make([]byte, 16)}
	sizes := make([]int, len(buffers))
	if err := c.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	begin := time.Now()
	n, err := c.ReadBatch(buffers, sizes)
	if err != nil || n != 2 || sizes[0] != 20000 || sizes[1] != 40000 {
		t.Fatalf("batch=%d sizes=%v err=%v", n, sizes, err)
	}
	if time.Since(begin) >= 500*time.Millisecond {
		t.Fatal("partial batch waited for another packet")
	}
	if !bytes.Equal(buffers[0][:sizes[0]], data[:20000]) || !bytes.Equal(buffers[1][:sizes[1]], data[20000:]) {
		t.Fatal("stream corrupted")
	}
	c.SetReadDeadline(time.Now().Add(5 * time.Millisecond))
	if _, err := c.ReadBatch(buffers, sizes); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatal(err)
	}
	c.SetReadDeadline(time.Time{})
	if err := s.Input(tcpPacket(60101, server, 17, []byte("tail"))); err != nil {
		t.Fatal(err)
	}
	if n, err := c.ReadBatch(buffers, sizes); err != nil || n != 1 || string(buffers[0][:sizes[0]]) != "tail" {
		t.Fatal(n, err)
	}
	if _, err := c.ReadBatch(buffers, sizes); !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
}

func TestTCPReadBatchBlockedClose(t *testing.T) {
	s, c, _, _ := established(t)
	done := make(chan error, 1)
	go func() { _, err := c.ReadBatch([][]byte{make([]byte, 16)}, make([]int, 1)); done <- err }()
	s.Close()
	select {
	case err := <-done:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked batch survived stack close")
	}
}
