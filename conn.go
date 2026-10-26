package lwip

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"runtime"
	"sync"
	"time"
	"unsafe"
)

type Conn struct {
	s                               *Stack
	id                              uint64
	udp                             bool
	source, destination             netip.AddrPort
	readMu, writeMu                 sync.Mutex
	readWake, writeWake             chan struct{}
	readDeadline, writeDeadline     time.Time
	closed, readClosed, writeClosed bool
}

func (c *Conn) IsUDP() bool                 { return c.udp }
func (c *Conn) Source() netip.AddrPort      { return c.source }
func (c *Conn) Destination() netip.AddrPort { return c.destination }
func (c *Conn) LocalAddr() net.Addr {
	if c.udp {
		return net.UDPAddrFromAddrPort(c.source)
	}
	return net.TCPAddrFromAddrPort(c.source)
}
func (c *Conn) RemoteAddr() net.Addr {
	if c.udp {
		return net.UDPAddrFromAddrPort(c.destination)
	}
	return net.TCPAddrFromAddrPort(c.destination)
}
func (c *Conn) wake() {
	select {
	case c.readWake <- struct{}{}:
	default:
	}
	select {
	case c.writeWake <- struct{}{}:
	default:
	}
}
func wait(wake <-chan struct{}, deadline time.Time) error {
	if deadline.IsZero() {
		<-wake
		return nil
	}
	d := time.Until(deadline)
	if d <= 0 {
		return os.ErrDeadlineExceeded
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-wake:
		return nil
	case <-t.C:
		return os.ErrDeadlineExceeded
	}
}
func pointer(p []byte) unsafe.Pointer {
	if len(p) == 0 {
		return nil
	}
	return unsafe.Pointer(&p[0])
}

func (c *Conn) read(p []byte, address *[40]byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	if len(p) > 65535 {
		p = p[:65535]
	}
	for {
		core.Lock()
		if c.closed || c.readClosed {
			core.Unlock()
			return 0, net.ErrClosed
		}
		d := c.readDeadline
		if !d.IsZero() && !time.Now().Before(d) {
			core.Unlock()
			return 0, os.ErrDeadlineExceeded
		}
		n := core.a.read(c.id, pointer(p), uint32(len(p)), unsafe.Pointer(address))
		runtime.KeepAlive(p)
		runtime.KeepAlive(address)
		c.s.pump()
		core.Unlock()
		if n > 0 || (n == 0 && c.udp) {
			return int(n), nil
		}
		if n == 0 {
			return 0, io.EOF
		}
		if n != -1 {
			return 0, nativeError(n)
		}
		if err := wait(c.readWake, d); err != nil {
			return 0, err
		}
	}
}
func (c *Conn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return c.read(p, nil)
}
func (c *Conn) ReadPacket(p []byte) (int, netip.AddrPort, netip.AddrPort, error) {
	if !c.udp {
		return 0, netip.AddrPort{}, netip.AddrPort{}, errors.New("not a UDP connection")
	}
	var address [40]byte
	n, err := c.read(p, &address)
	if err != nil {
		return n, netip.AddrPort{}, netip.AddrPort{}, err
	}
	src, dst := decodeAddress(address[:])
	return n, src, dst, nil
}
func (c *Conn) write(p []byte, address *[40]byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	total := 0
	for total < len(p) || (c.udp && total == 0) {
		core.Lock()
		if c.closed || c.writeClosed {
			core.Unlock()
			return total, net.ErrClosed
		}
		d := c.writeDeadline
		if !d.IsZero() && !time.Now().Before(d) {
			core.Unlock()
			return total, os.ErrDeadlineExceeded
		}
		remaining := p[total:]
		if !c.udp && len(remaining) > 65535 {
			remaining = remaining[:65535]
		}
		n := core.a.write(c.id, pointer(remaining), uint32(len(remaining)), unsafe.Pointer(address))
		runtime.KeepAlive(p)
		runtime.KeepAlive(address)
		c.s.pump()
		core.Unlock()
		if n >= 0 {
			total += int(n)
			if c.udp {
				return total, nil
			}
			continue
		}
		if n != -1 {
			return total, nativeError(n)
		}
		if err := wait(c.writeWake, d); err != nil {
			return total, err
		}
	}
	return total, nil
}
func (c *Conn) Write(p []byte) (int, error) {
	if c.udp {
		return 0, errors.New("use WritePacket for UDP")
	}
	return c.write(p, nil)
}

// WritePacket sends a response to the original client, using source as the
// source IP/port. This supports endpoint-independent UDP NAT replies.
func (c *Conn) WritePacket(p []byte, source netip.AddrPort) error {
	if !c.udp || !source.IsValid() {
		return errors.New("invalid UDP source")
	}
	var address [40]byte
	a := source.Addr().Unmap()
	if a.Is4() {
		v := a.As4()
		copy(address[16:20], v[:])
		address[36] = 4
	} else {
		v := a.As16()
		copy(address[16:32], v[:])
		address[36] = 6
	}
	binary.LittleEndian.PutUint16(address[34:], source.Port())
	_, err := c.write(p, &address)
	return err
}

func (c *Conn) closeLocked(abort bool) {
	if c.closed {
		return
	}
	c.closed = true
	var how uint32
	if abort {
		how = 1
	}
	core.a.close(c.id, how)
	delete(c.s.conns, c.id)
	c.wake()
}
func (c *Conn) Close() error {
	core.Lock()
	defer core.Unlock()
	c.closeLocked(false)
	if !c.s.closed {
		c.s.pump()
	}
	return nil
}
func (c *Conn) Abort() error {
	core.Lock()
	defer core.Unlock()
	c.closeLocked(true)
	if !c.s.closed {
		c.s.pump()
	}
	return nil
}
func (c *Conn) CloseRead() error {
	core.Lock()
	defer core.Unlock()
	if c.closed {
		return net.ErrClosed
	}
	if c.readClosed {
		return nil
	}
	n := core.a.shutdown(c.id, 1, 0)
	if n == 0 {
		c.readClosed = true
	}
	c.wake()
	c.s.pump()
	return nativeError(n)
}
func (c *Conn) CloseWrite() error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	for {
		core.Lock()
		if c.closed {
			core.Unlock()
			return net.ErrClosed
		}
		if c.writeClosed {
			core.Unlock()
			return nil
		}
		d := c.writeDeadline
		n := core.a.shutdown(c.id, 0, 1)
		if n == 0 {
			c.writeClosed = true
			c.wake()
		}
		c.s.pump()
		core.Unlock()
		if n != -1 {
			return nativeError(n)
		}
		if err := wait(c.writeWake, d); err != nil {
			return err
		}
	}
}
func (c *Conn) HandshakeSuccess() error      { return nil }
func (c *Conn) HandshakeFailure(error) error { return c.Abort() }
func (c *Conn) SetDeadline(t time.Time) error {
	core.Lock()
	defer core.Unlock()
	if c.closed {
		return net.ErrClosed
	}
	c.readDeadline = t
	c.writeDeadline = t
	c.wake()
	return nil
}
func (c *Conn) SetReadDeadline(t time.Time) error {
	core.Lock()
	defer core.Unlock()
	if c.closed {
		return net.ErrClosed
	}
	c.readDeadline = t
	c.wake()
	return nil
}
func (c *Conn) SetWriteDeadline(t time.Time) error {
	core.Lock()
	defer core.Unlock()
	if c.closed {
		return net.ErrClosed
	}
	c.writeDeadline = t
	c.wake()
	return nil
}
