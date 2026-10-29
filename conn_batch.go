package lwip

import (
	"errors"
	"io"
	"net"
	"os"
	"runtime"
	"time"
)

// ReadBatch reads TCP stream data into caller-owned buffers, returning the
// number filled and their lengths in sizes. It waits for the first bytes only;
// subsequent buffers receive data already queued in the native core. Each buffer
// receives at most 65535 bytes. Native code never retains the supplied storage.
// A partial batch returns nil error; EOF/reset is reported by the next read.
// Supply 1 to 16 nonempty buffers and at least that many size entries.
func (c *Conn) ReadBatch(buffers [][]byte, sizes []int) (int, error) {
	if c.udp {
		return 0, errors.New("ReadBatch requires a TCP connection")
	}
	if len(buffers) == 0 || len(buffers) > 16 || len(sizes) < len(buffers) {
		return 0, io.ErrShortBuffer
	}
	for _, p := range buffers {
		if len(p) == 0 {
			return 0, io.ErrShortBuffer
		}
	}
	c.readMu.Lock()
	defer c.readMu.Unlock()
	for {
		core.Lock()
		if c.closed || c.readClosed {
			core.Unlock()
			return 0, net.ErrClosed
		}
		deadline := c.readDeadline
		if !deadline.IsZero() && !time.Now().Before(deadline) {
			core.Unlock()
			return 0, os.ErrDeadlineExceeded
		}
		count := 0
		var result int32
		for _, p := range buffers {
			if len(p) > 65535 {
				p = p[:65535]
			}
			result = core.a.read(c.id, pointer(p), uint32(len(p)), nil)
			runtime.KeepAlive(p)
			if result <= 0 {
				break
			}
			sizes[count] = int(result)
			count++
			// sl_read fills up to its cap while holding core. A short TCP read
			// exhausted the queue; another native call can only return would-block
			// or EOF until core is unlocked for input.
			if int(result) < len(p) {
				break
			}
		}
		if count > 0 {
			c.s.pump()
		}
		core.Unlock()
		if count > 0 {
			return count, nil
		}
		if result == 0 {
			return 0, io.EOF
		}
		if result != -1 {
			return 0, nativeError(result)
		}
		if err := wait(c.readWake, deadline); err != nil {
			return 0, err
		}
	}
}
