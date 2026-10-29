// Package lwip embeds HEV lwIP behind a serialized native core. It operates on
// IP packets and never creates interfaces, changes routes, or opens upstream sockets.
package lwip

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"runtime"
	"sync"
	"time"
	"unsafe"
)

type Options struct {
	MTU            uint32
	IPv4, IPv6     netip.Addr
	MaxConnections uint32
	// TCPGSO requires an output consumer that understands Packet.GSOSize.
	// It is opt-in and scoped to this stack's netif.
	TCPGSO bool
	Accept func(*Conn)
}

var core struct {
	sync.Mutex
	a      nativeAPI
	stacks map[uint64]*Stack
	stop   chan struct{}
}

type Stack struct {
	id         uint64
	options    Options
	conns      map[uint64]*Conn
	events     []byte
	outputWake chan struct{}
	done       chan struct{}
	closed     bool // all native state and connection fields are protected by core
}

func New(options Options) (*Stack, error) {
	if options.MTU < 576 || options.MTU > 65535 {
		return nil, errors.New("lwIP MTU must be between 576 and 65535")
	}
	if options.Accept == nil {
		return nil, errors.New("lwIP requires an accept handler")
	}
	core.Lock()
	defer core.Unlock()
	if core.a.abi == nil {
		a, err := loadNative()
		if err != nil {
			return nil, err
		}
		if v := a.abi(); v != nativeABIVersion {
			return nil, fmt.Errorf("unsupported sing-lwip ABI: %d", v)
		}
		core.a = a
		core.stacks = make(map[uint64]*Stack)
	}
	var cfg [32]byte
	binary.LittleEndian.PutUint32(cfg[:], options.MTU)
	binary.LittleEndian.PutUint32(cfg[4:], options.MaxConnections)
	if options.TCPGSO {
		binary.LittleEndian.PutUint32(cfg[12:], 1)
	}
	if options.IPv4.Is4() {
		a := options.IPv4.As4()
		copy(cfg[8:12], a[:])
	} else {
		copy(cfg[8:12], []byte{127, 0, 0, 1})
	}
	if options.IPv6.Is6() {
		a := options.IPv6.As16()
		copy(cfg[16:], a[:])
	} else {
		cfg[31] = 1
	}
	id := core.a.create(unsafe.Pointer(&cfg[0]))
	runtime.KeepAlive(cfg)
	if id == 0 {
		return nil, errors.New("initialize lwIP netif/listeners")
	}
	s := &Stack{id: id, options: options, conns: make(map[uint64]*Conn), events: make([]byte, 16384), outputWake: make(chan struct{}, 1), done: make(chan struct{})}
	if len(core.stacks) == 0 {
		core.stop = make(chan struct{})
		go runTimers(core.stop)
	}
	core.stacks[id] = s
	return s, nil
}

func runTimers(stop chan struct{}) {
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			core.Lock()
			if core.stop != stop {
				core.Unlock()
				return
			}
			core.a.tick()
			for _, s := range core.stacks {
				s.pump()
			}
			core.Unlock()
		}
	}
}

func (s *Stack) Input(packet []byte) error {
	if len(packet) < 20 || len(packet) > 65535 {
		return errors.New("invalid IP packet size")
	}
	core.Lock()
	defer core.Unlock()
	if s.closed {
		return net.ErrClosed
	}
	n := core.a.input(s.id, unsafe.Pointer(&packet[0]), uint32(len(packet)))
	runtime.KeepAlive(packet)
	s.pump()
	return nativeError(n)
}

// Packet is an output IP packet with a complete checksum. GSOSize, when nonzero,
// specifies the TCP payload size per wire segment; the consumer must segment an
// oversized packet in software or supply matching device offload metadata.
type Packet struct {
	Data    []byte
	GSOSize uint16
}

// ReadPackets reads ordinary output packets. GSO consumers use ReadPacketBatch.
// Returned slices borrow buffer until the caller reuses it.
func (s *Stack) ReadPackets(buffer []byte) ([][]byte, error) {
	if s.options.TCPGSO {
		return nil, errors.New("TCP GSO output requires ReadPacketBatch")
	}
	batch, err := s.ReadPacketBatch(buffer, nil)
	if err != nil {
		return nil, err
	}
	packets := make([][]byte, len(batch))
	for i, p := range batch {
		packets[i] = p.Data
	}
	return packets, nil
}

// ReadPacketBatch waits for output, reusing the supplied packet slice. Data
// borrows buffer until the next read; buffer must be at least 65543 bytes.
func (s *Stack) ReadPacketBatch(buffer []byte, packets []Packet) ([]Packet, error) {
	if len(buffer) < 65543 {
		return nil, io.ErrShortBuffer
	}
	clear(packets)
	packets = packets[:0]
	for {
		core.Lock()
		if s.closed {
			core.Unlock()
			return nil, net.ErrClosed
		}
		n := core.a.output(s.id, unsafe.Pointer(&buffer[0]), uint32(len(buffer)))
		runtime.KeepAlive(buffer)
		core.Unlock()
		if n > 0 {
			for p := buffer[:n]; len(p) > 0; {
				if len(p) < 8 {
					return nil, errors.New("invalid native packet record")
				}
				length := int(binary.LittleEndian.Uint32(p))
				gso := binary.LittleEndian.Uint16(p[4:])
				if binary.LittleEndian.Uint16(p[6:]) != 0 || (gso != 0 && !s.options.TCPGSO) {
					return nil, errors.New("invalid native packet metadata")
				}
				p = p[8:]
				if length < 20 || length > 65535 || length > len(p) {
					return nil, errors.New("invalid native packet length")
				}
				packets = append(packets, Packet{Data: p[:length:length], GSOSize: gso})
				p = p[length:]
			}
			return packets, nil
		}
		select {
		case <-s.outputWake:
		case <-s.done:
			return nil, net.ErrClosed
		}
	}
}

func (s *Stack) pump() {
	s.wakeOutput()
	for {
		n := core.a.drain(s.id, unsafe.Pointer(&s.events[0]), uint32(len(s.events)))
		if n == 0 {
			return
		}
		for p := s.events[:n]; len(p) >= 16; {
			kind, size, id := binary.LittleEndian.Uint32(p), binary.LittleEndian.Uint32(p[4:]), binary.LittleEndian.Uint64(p[8:])
			data := p[16 : 16+size]
			p = p[16+size:]
			switch kind {
			case 1, 2:
				c := &Conn{s: s, id: id, udp: kind == 2, readWake: make(chan struct{}, 1), writeWake: make(chan struct{}, 1)}
				c.source, c.destination = decodeAddress(data)
				s.conns[id] = c
				go s.options.Accept(c)
			case 3:
				if c := s.conns[id]; c != nil {
					c.wake()
				}
			}
		}
		// ABI-2 drain reserves 56 bytes for the largest event before each
		// iteration. A shorter batch exhausted the queue while core is held;
		// avoid a second native call just to obtain an empty result.
		if int(n)+56 <= len(s.events) {
			return
		}
	}
}

func (s *Stack) wakeOutput() {
	select {
	case s.outputWake <- struct{}{}:
	default:
	}
}

func (s *Stack) ResetUDP() {
	core.Lock()
	defer core.Unlock()
	if s.closed {
		return
	}
	for _, c := range s.conns {
		if c.udp {
			c.closeLocked(true)
		}
	}
	s.pump()
}

func (s *Stack) Close() error {
	core.Lock()
	defer core.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	close(s.done)
	for _, c := range s.conns {
		c.closed = true
		c.wake()
	}
	core.a.destroy(s.id)
	clear(s.conns)
	delete(core.stacks, s.id)
	if len(core.stacks) == 0 {
		close(core.stop)
		core.stop = nil
	}
	return nil
}

func decodeAddress(p []byte) (netip.AddrPort, netip.AddrPort) {
	var src, dst netip.Addr
	if p[36] == 4 {
		src = netip.AddrFrom4([4]byte(p[:4]))
		dst = netip.AddrFrom4([4]byte(p[16:20]))
	} else {
		src = netip.AddrFrom16([16]byte(p[:16]))
		dst = netip.AddrFrom16([16]byte(p[16:32]))
	}
	return netip.AddrPortFrom(src, binary.LittleEndian.Uint16(p[32:])), netip.AddrPortFrom(dst, binary.LittleEndian.Uint16(p[34:]))
}

func nativeError(n int32) error {
	switch n {
	case 0:
		return nil
	case -2, -6:
		return net.ErrClosed
	case -3:
		return ErrReset
	case -4:
		return errors.New("invalid lwIP operation or packet")
	case -5:
		return errors.New("lwIP buffer exhausted")
	default:
		return fmt.Errorf("lwIP error: %d", n)
	}
}

var ErrReset = errors.New("connection reset by peer")
