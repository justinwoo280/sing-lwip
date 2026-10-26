//go:build cgo || with_purego

package lwip

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net/netip"
	"os"
	"testing"
	"time"
)

func checksum(p []byte) uint16 {
	var n uint32
	for len(p) >= 2 {
		n += uint32(binary.BigEndian.Uint16(p))
		p = p[2:]
	}
	if len(p) > 0 {
		n += uint32(p[0]) << 8
	}
	for n>>16 != 0 {
		n = (n & 65535) + (n >> 16)
	}
	return ^uint16(n)
}
func ipPacket(proto byte, payload []byte) []byte {
	p := make([]byte, 20+len(payload))
	p[0] = 0x45
	p[8] = 64
	p[9] = proto
	binary.BigEndian.PutUint16(p[2:], uint16(len(p)))
	copy(p[12:16], []byte{198, 18, 0, 10})
	copy(p[16:20], []byte{198, 18, 0, 20})
	copy(p[20:], payload)
	binary.BigEndian.PutUint16(p[10:], checksum(p[:20]))
	return p
}
func udpPacket(port uint16, payload []byte) []byte {
	u := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint16(u, 40000)
	binary.BigEndian.PutUint16(u[2:], port)
	binary.BigEndian.PutUint16(u[4:], uint16(len(u)))
	copy(u[8:], payload)
	return ipPacket(17, u)
}
func tcpPacket(seq, ack uint32, flags byte, payload []byte) []byte {
	p := make([]byte, 20+len(payload))
	binary.BigEndian.PutUint16(p, 40001)
	binary.BigEndian.PutUint16(p[2:], 80)
	binary.BigEndian.PutUint32(p[4:], seq)
	binary.BigEndian.PutUint32(p[8:], ack)
	p[12] = 5 << 4
	p[13] = flags
	binary.BigEndian.PutUint16(p[14:], 65535)
	copy(p[20:], payload)
	ip := ipPacket(6, p)
	pseudo := append([]byte(nil), ip[12:20]...)
	pseudo = append(pseudo, 0, 6, byte(len(p)>>8), byte(len(p)))
	pseudo = append(pseudo, p...)
	binary.BigEndian.PutUint16(ip[36:], checksum(pseudo))
	return ip
}
func output(t *testing.T, s *Stack) <-chan []byte {
	t.Helper()
	ch := make(chan []byte, 1024)
	go func() {
		defer close(ch)
		b := make([]byte, 1<<20)
		for {
			packets, e := s.ReadPackets(b)
			if e != nil {
				return
			}
			for _, p := range packets {
				ch <- bytes.Clone(p)
			}
		}
	}()
	return ch
}
func next(t *testing.T, ch <-chan []byte, proto byte) []byte {
	t.Helper()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case p, ok := <-ch:
			if !ok {
				t.Fatal("output closed")
			}
			if p[0]>>4 == 4 && p[9] == proto {
				return p
			}
		case <-timer.C:
			t.Fatal("output timeout")
		}
	}
}
func TestMultipleStacksUDPAndRestart(t *testing.T) {
	for round := 0; round < 4; round++ {
		var stacks []*Stack
		var outputs []<-chan []byte
		for i := 0; i < 2; i++ {
			marker := byte('A' + i)
			s, e := New(Options{MTU: 1500, Accept: func(c *Conn) {
				defer c.Close()
				b := make([]byte, 65535)
				for {
					n, _, dst, e := c.ReadPacket(b)
					if e != nil {
						return
					}
					reply := append([]byte{marker}, b[:n]...)
					if e = c.WritePacket(reply, dst); e != nil {
						return
					}
				}
			}})
			if e != nil {
				t.Fatal(e)
			}
			stacks = append(stacks, s)
			outputs = append(outputs, output(t, s))
		}
		for i, s := range stacks {
			if e := s.Input(udpPacket(1000, []byte("one"))); e != nil {
				t.Fatal(e)
			}
			p := next(t, outputs[i], 17)
			if string(p[28:]) != string([]byte{byte('A' + i)})+"one" {
				t.Fatalf("cross-stack reply %q", p[28:])
			}
		}
		stacks[0].Close()
		for _, port := range []uint16{1001, 2002, 3003} {
			if e := stacks[1].Input(udpPacket(port, nil)); e != nil {
				t.Fatal(e)
			}
			p := next(t, outputs[1], 17)
			if binary.BigEndian.Uint16(p[20:]) != port || string(p[28:]) != "B" {
				t.Fatal("per-packet address lost")
			}
		}
		stacks[1].ResetUDP()
		if e := stacks[1].Input(udpPacket(4040, []byte("reset"))); e != nil {
			t.Fatal(e)
		}
		if p := next(t, outputs[1], 17); string(p[28:]) != "Breset" {
			t.Fatal("reset failed")
		}
		stacks[1].Close()
	}
}

func established(t *testing.T) (*Stack, *Conn, <-chan []byte, uint32) {
	t.Helper()
	accept := make(chan *Conn, 1)
	s, e := New(Options{MTU: 1500, Accept: func(c *Conn) { accept <- c }})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	out := output(t, s)
	if e = s.Input(tcpPacket(100, 0, 2, nil)); e != nil {
		t.Fatal(e)
	}
	syn := next(t, out, 6)
	server := binary.BigEndian.Uint32(syn[24:]) + 1
	if syn[33]&0x12 != 0x12 {
		t.Fatal("not SYN ACK")
	}
	if e = s.Input(tcpPacket(101, server, 16, nil)); e != nil {
		t.Fatal(e)
	}
	select {
	case c := <-accept:
		return s, c, out, server
	case <-time.After(time.Second):
		t.Fatal("accept timeout")
	}
	panic("unreachable")
}
func TestTCPServerHalfClose(t *testing.T) {
	s, c, out, server := established(t)
	if _, e := c.Write([]byte("abc")); e != nil {
		t.Fatal(e)
	}
	if e := c.CloseWrite(); e != nil {
		t.Fatal(e)
	}
	var got []byte
	fin := false
	for !fin {
		p := next(t, out, 6)
		h := int(p[32]>>4) * 4
		got = append(got, p[20+h:]...)
		fin = p[33]&1 != 0
	}
	if string(got) != "abc" {
		t.Fatalf("data before FIN: %q", got)
	}
	if e := s.Input(tcpPacket(101, server+4, 16, []byte("reply"))); e != nil {
		t.Fatal(e)
	}
	var b [32]byte
	if n, e := c.Read(b[:]); e != nil || string(b[:n]) != "reply" {
		t.Fatalf("read after half close: %d %v", n, e)
	}
	if e := s.Input(tcpPacket(106, server+4, 17, nil)); e != nil {
		t.Fatal(e)
	}
	if _, e := c.Read(b[:]); !errors.Is(e, io.EOF) {
		t.Fatalf("EOF after FIN: %v", e)
	}
	if e := c.Close(); e != nil {
		t.Fatal(e)
	}
}
func TestDeadlinesAndBlockedClose(t *testing.T) {
	s, c, _, _ := established(t)
	c.SetReadDeadline(time.Now().Add(5 * time.Millisecond))
	if _, e := c.Read(make([]byte, 1)); !errors.Is(e, os.ErrDeadlineExceeded) {
		t.Fatal(e)
	}
	c.SetReadDeadline(time.Time{})
	c.SetWriteDeadline(time.Now().Add(10 * time.Millisecond))
	if _, e := c.Write(make([]byte, 1<<20)); !errors.Is(e, os.ErrDeadlineExceeded) {
		t.Fatalf("write backpressure deadline: %v", e)
	}
	done := make(chan error, 1)
	go func() { _, e := c.Read(make([]byte, 1)); done <- e }()
	s.Close()
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("blocked read survived close")
		}
	case <-time.After(time.Second):
		t.Fatal("blocked close")
	}
}
func TestIPv4SourceRepresentation(t *testing.T) {
	// Covers the byte order in the stable native metadata ABI.
	p := make([]byte, 40)
	copy(p, []byte{192, 0, 2, 1})
	copy(p[16:], []byte{203, 0, 113, 7})
	p[36] = 4
	binary.LittleEndian.PutUint16(p[32:], 1234)
	binary.LittleEndian.PutUint16(p[34:], 443)
	s, d := decodeAddress(p)
	if s != netip.MustParseAddrPort("192.0.2.1:1234") || d != netip.MustParseAddrPort("203.0.113.7:443") {
		t.Fatal(s, d)
	}
}

func fragmentedUDP(v6 bool, payload []byte, fragmentSize int) [][]byte {
	u := udpPacket(1234, payload)[20:]
	var src, dst [16]byte
	src = netip.MustParseAddr("fd00::10").As16()
	dst = netip.MustParseAddr("fd00::20").As16()
	if v6 {
		pseudo := append(append([]byte(nil), src[:]...), dst[:]...)
		pseudo = append(pseudo, 0, 0, byte(len(u)>>8), byte(len(u)), 0, 0, 0, 17)
		pseudo = append(pseudo, u...)
		sum := checksum(pseudo)
		if sum == 0 {
			sum = 65535
		}
		binary.BigEndian.PutUint16(u[6:], sum)
	}
	var packets [][]byte
	for offset := 0; offset < len(u); {
		n := min(fragmentSize, len(u)-offset)
		more := offset+n < len(u)
		if v6 {
			p := make([]byte, 48+n)
			p[0] = 0x60
			p[6] = 44
			p[7] = 64
			binary.BigEndian.PutUint16(p[4:], uint16(8+n))
			copy(p[8:24], src[:])
			copy(p[24:40], dst[:])
			p[40] = 17
			flags := uint16(offset)
			if more {
				flags |= 1
			}
			binary.BigEndian.PutUint16(p[42:], flags)
			binary.BigEndian.PutUint32(p[44:], 42)
			copy(p[48:], u[offset:offset+n])
			packets = append(packets, p)
		} else {
			p := ipPacket(17, u[offset:offset+n])
			binary.BigEndian.PutUint16(p[4:], 42)
			flags := uint16(offset / 8)
			if more {
				flags |= 0x2000
			}
			binary.BigEndian.PutUint16(p[6:], flags)
			p[10] = 0
			p[11] = 0
			binary.BigEndian.PutUint16(p[10:], checksum(p[:20]))
			packets = append(packets, p)
		}
		offset += n
	}
	return packets
}

func TestReassemblyIsolationAndPartialTeardown(t *testing.T) {
	for _, v6 := range []bool{false, true} {
		for _, size := range []struct{ payload, fragment int }{{24, 16}, {59972, 40000}} {
			// Large fragments retain pooled custom pbufs across partial teardown.
			received := []chan []byte{make(chan []byte, 4), make(chan []byte, 4)}
			var stacks []*Stack
			for i := 0; i < 2; i++ {
				ch := received[i]
				s, e := New(Options{MTU: 1500, Accept: func(c *Conn) {
					defer c.Close()
					b := make([]byte, 65535)
					for {
						n, _, _, e := c.ReadPacket(b)
						if e != nil {
							return
						}
						ch <- bytes.Clone(b[:n])
					}
				}})
				if e != nil {
					t.Fatal(e)
				}
				stacks = append(stacks, s)
				defer s.Close()
			}
			a, b := fragmentedUDP(v6, bytes.Repeat([]byte("a"), size.payload), size.fragment), fragmentedUDP(v6, bytes.Repeat([]byte("b"), size.payload), size.fragment)
			for i := range a {
				if e := stacks[0].Input(a[i]); e != nil {
					t.Fatal(e)
				}
				if e := stacks[1].Input(b[i]); e != nil {
					t.Fatal(e)
				}
			}
			for i, ch := range received {
				select {
				case p := <-ch:
					if !bytes.Equal(p, bytes.Repeat([]byte{byte('a' + i)}, size.payload)) {
						t.Fatalf("cross-netif reassembly v6=%v payload=%d", v6, size.payload)
					}
				case <-time.After(time.Second):
					t.Fatalf("reassembly timeout v6=%v", v6)
				}
			}
			stacks[0].Input(a[0])
			stacks[1].Input(b[0])
			stacks[0].Close()
			for _, p := range b[1:] {
				if e := stacks[1].Input(p); e != nil {
					t.Fatal(e)
				}
			}
			select {
			case p := <-received[1]:
				if !bytes.Equal(p, bytes.Repeat([]byte("b"), size.payload)) {
					t.Fatal("teardown corrupted other netif")
				}
			case <-time.After(time.Second):
				t.Fatal("teardown discarded other netif fragments")
			}
			stacks[1].Close()
		}
	}
}
