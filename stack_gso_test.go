//go:build cgo || with_purego

package lwip

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net/netip"
	"testing"
	"time"
)

func gsoTCP(v6 bool, seq, ack uint32, flags byte, wnd, mss uint16) []byte {
	ipLen, tcpLen := 20, 20
	if v6 {
		ipLen = 40
	}
	if flags&2 != 0 {
		tcpLen += 4
	}
	p := make([]byte, ipLen+tcpLen)
	if v6 {
		p[0], p[6], p[7] = 0x60, 6, 64
		binary.BigEndian.PutUint16(p[4:], uint16(tcpLen))
		a, b := netip.MustParseAddr("fd00::10").As16(), netip.MustParseAddr("fd00::20").As16()
		copy(p[8:24], a[:])
		copy(p[24:40], b[:])
	} else {
		p[0], p[8], p[9] = 0x45, 64, 6
		binary.BigEndian.PutUint16(p[2:], uint16(len(p)))
		copy(p[12:20], []byte{198, 18, 0, 10, 198, 18, 0, 20})
		binary.BigEndian.PutUint16(p[10:], checksum(p[:20]))
	}
	tcp := p[ipLen:]
	binary.BigEndian.PutUint16(tcp, 40001)
	binary.BigEndian.PutUint16(tcp[2:], 80)
	binary.BigEndian.PutUint32(tcp[4:], seq)
	binary.BigEndian.PutUint32(tcp[8:], ack)
	tcp[12], tcp[13] = byte(tcpLen/4)<<4, flags
	binary.BigEndian.PutUint16(tcp[14:], wnd)
	if tcpLen > 20 {
		copy(tcp[20:], []byte{2, 4, byte(mss >> 8), byte(mss)})
	}
	binary.BigEndian.PutUint16(tcp[16:], gsoTCPChecksum(p))
	return p
}

func gsoTCPChecksum(p []byte) uint16 {
	ipLen := 20
	var pseudo []byte
	if p[0]>>4 == 6 {
		ipLen = 40
		pseudo = append(pseudo, p[8:40]...)
		pseudo = append(pseudo, 0, 0, byte((len(p)-40)>>8), byte(len(p)-40), 0, 0, 0, 6)
	} else {
		pseudo = append(pseudo, p[12:20]...)
		pseudo = append(pseudo, 0, 6, byte((len(p)-20)>>8), byte(len(p)-20))
	}
	return checksum(append(pseudo, p[ipLen:]...))
}

type gsoPeer struct {
	s      *Stack
	c      *Conn
	out    <-chan Packet
	v6     bool
	server uint32
	mtu    int
	mss    uint16
	gso    bool
}

func newGSOPeer(t *testing.T, v6, enabled bool, mtu int, mss uint16) *gsoPeer {
	t.Helper()
	accepted := make(chan *Conn, 1)
	s, err := New(Options{MTU: uint32(mtu), TCPGSO: enabled, Accept: func(c *Conn) { accepted <- c }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	output := make(chan Packet, 1024)
	go func() {
		defer close(output)
		b := make([]byte, 1<<20)
		var packets []Packet
		for {
			var e error
			packets, e = s.ReadPacketBatch(b, packets)
			if e != nil {
				return
			}
			for _, p := range packets {
				p.Data = bytes.Clone(p.Data)
				output <- p
			}
		}
	}()
	peer := &gsoPeer{s: s, out: output, v6: v6, mtu: mtu, gso: enabled}
	ipLen := 20
	if v6 {
		ipLen = 40
	}
	peer.mss = min(mss, 8191, uint16(mtu-ipLen-20))
	if err := s.Input(gsoTCP(v6, 100, 0, 2, 65535, mss)); err != nil {
		t.Fatal(err)
	}
	syn := peer.next(t)
	if syn.GSOSize != 0 || syn.Data[ipLen+13]&0x12 != 0x12 || len(syn.Data) > mtu {
		t.Fatal("invalid SYN ACK offload metadata")
	}
	peer.server = binary.BigEndian.Uint32(syn.Data[ipLen+4:]) + 1
	peer.ack(t, peer.server, 65535)
	select {
	case peer.c = <-accepted:
	case <-time.After(2 * time.Second):
		t.Fatal("GSO accept timeout")
	}
	peer.c.SetDeadline(time.Now().Add(5 * time.Second))
	return peer
}

func (p *gsoPeer) ack(t *testing.T, seq uint32, wnd uint16) {
	t.Helper()
	if err := p.s.Input(gsoTCP(p.v6, 101, seq, 16, wnd, 0)); err != nil {
		t.Fatal(err)
	}
}

func (p *gsoPeer) next(t *testing.T) Packet {
	t.Helper()
	select {
	case packet, ok := <-p.out:
		if !ok {
			t.Fatal("GSO output closed")
		}
		data := packet.Data
		ipLen := 20
		if p.v6 {
			ipLen = 40
			if data[6] != 6 || int(binary.BigEndian.Uint16(data[4:])) != len(data)-40 {
				t.Fatal("GSO was IPv6 fragmented or has an invalid length")
			}
		} else if data[9] != 6 || binary.BigEndian.Uint16(data[6:])&0x3fff != 0 || int(binary.BigEndian.Uint16(data[2:])) != len(data) || checksum(data[:20]) != 0 {
			t.Fatal("GSO was IPv4 fragmented or has an invalid IP checksum/length")
		}
		if len(data) < ipLen+20 || len(data) > 65535 || gsoTCPChecksum(data) != 0 {
			t.Fatal("invalid native full TCP checksum/length")
		}
		if (!p.gso && packet.GSOSize != 0) || (len(data) > p.mtu && packet.GSOSize == 0) {
			t.Fatal("missing or cross-netif GSO metadata")
		}
		if packet.GSOSize != 0 && packet.GSOSize != p.mss {
			t.Fatalf("wire MSS changed: %d != %d", packet.GSOSize, p.mss)
		}
		return packet
	case <-time.After(2 * time.Second):
		t.Fatal("GSO output timeout")
	}
	panic("unreachable")
}

func (p *gsoPeer) receive(t *testing.T, want []byte, window uint16) bool {
	t.Helper()
	ipLen := 20
	if p.v6 {
		ipLen = 40
	}
	var got []byte
	merged := false
	for {
		packet := p.next(t)
		tcp := packet.Data[ipLen:]
		payload := tcp[int(tcp[12]>>4)*4:]
		if len(payload) == 0 && tcp[13]&1 == 0 {
			continue
		}
		seq := binary.BigEndian.Uint32(tcp[4:])
		if seq != p.server+uint32(len(got)) || len(payload) > int(window) {
			t.Fatalf("GSO sequence/window violation: seq=%d want=%d payload=%d window=%d", seq, p.server+uint32(len(got)), len(payload), window)
		}
		merged = merged || (packet.GSOSize > 0 && len(payload) > int(packet.GSOSize))
		got = append(got, payload...)
		fin := tcp[13]&1 != 0
		ack := p.server + uint32(len(got))
		if fin {
			ack++
		}
		p.ack(t, ack, window)
		if fin {
			if !bytes.Equal(got, want) {
				t.Fatalf("GSO stream/FIN: got=%d want=%d", len(got), len(want))
			}
			return merged
		}
	}
}

func TestNativeGSOMSSAndNetifIsolation(t *testing.T) {
	for _, v6 := range []bool{false, true} {
		for _, mtu := range []int{1500, 9000} {
			for _, mss := range []uint16{1200, 8900} {
				t.Run(fmt.Sprintf("v6=%v/mtu=%d/mss=%d", v6, mtu, mss), func(t *testing.T) {
					a, b := newGSOPeer(t, v6, true, mtu, mss), newGSOPeer(t, v6, false, mtu, mss)
					first, second := bytes.Repeat([]byte{0x31}, 60000), bytes.Repeat([]byte{0xa7}, 60000)
					for i, peer := range []*gsoPeer{a, b} {
						data := first
						if i == 1 {
							data = second
						}
						if _, err := peer.c.Write(data); err != nil {
							t.Fatal(err)
						}
						if err := peer.c.CloseWrite(); err != nil {
							t.Fatal(err)
						}
					}
					if !a.receive(t, first, 65535) {
						t.Fatal("native GSO did not produce aggregates")
					}
					a.s.Close()
					if b.receive(t, second, 65535) {
						t.Fatal("non-GSO netif produced aggregates")
					}
				})
			}
		}
	}
}

func TestNativeGSOZeroAndSmallWindow(t *testing.T) {
	for _, v6 := range []bool{false, true} {
		peer := newGSOPeer(t, v6, true, 1500, 1200)
		// A shrinking window must accompany a newer ACK, rather than an old
		// duplicate ACK whose smaller advertisement lwIP deliberately ignores.
		if _, err := peer.c.Write([]byte{0x11}); err != nil {
			t.Fatal(err)
		}
		peer.next(t)
		peer.server++
		peer.ack(t, peer.server, 0)
		data := bytes.Repeat([]byte{0x63}, 20000)
		if _, err := peer.c.Write(data); err != nil {
			t.Fatal(err)
		}
		if err := peer.c.CloseWrite(); err != nil {
			t.Fatal(err)
		}
		peer.ack(t, peer.server, 600) // less than one MSS, without waiting for persist
		if peer.receive(t, data, 600) {
			t.Fatal("aggregate exceeded the small receive window")
		}
	}
}
