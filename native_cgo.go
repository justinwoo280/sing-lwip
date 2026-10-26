//go:build cgo && !with_purego

package lwip

/*
#cgo CFLAGS: -O3 -I${SRCDIR} -I${SRCDIR}/third_party/lwip/src/include -I${SRCDIR}/third_party/lwip/src/ports/include
#include "bridge.h"
*/
import "C"
import "unsafe"

func loadNative() (nativeAPI, error) {
	return nativeAPI{
		abi:     func() uint32 { return uint32(C.sl_abi()) },
		create:  func(p unsafe.Pointer) uint64 { return uint64(C.sl_create(p)) },
		destroy: func(id uint64) { C.sl_destroy(C.uint64_t(id)) },
		input: func(id uint64, p unsafe.Pointer, n uint32) int32 {
			return int32(C.sl_input(C.uint64_t(id), p, C.uint32_t(n)))
		},
		tick: func() { C.sl_tick() },
		drain: func(id uint64, p unsafe.Pointer, n uint32) uint32 {
			return uint32(C.sl_drain(C.uint64_t(id), p, C.uint32_t(n)))
		},
		output: func(id uint64, p unsafe.Pointer, n uint32) uint32 {
			return uint32(C.sl_output(C.uint64_t(id), p, C.uint32_t(n)))
		},
		read: func(id uint64, p unsafe.Pointer, n uint32, a unsafe.Pointer) int32 {
			return int32(C.sl_read(C.uint64_t(id), p, C.uint32_t(n), a))
		},
		write: func(id uint64, p unsafe.Pointer, n uint32, a unsafe.Pointer) int32 {
			return int32(C.sl_write(C.uint64_t(id), p, C.uint32_t(n), a))
		},
		shutdown: func(id uint64, r, w uint32) int32 {
			return int32(C.sl_shutdown(C.uint64_t(id), C.uint32_t(r), C.uint32_t(w)))
		},
		close: func(id uint64, abort uint32) { C.sl_close(C.uint64_t(id), C.uint32_t(abort)) },
	}, nil
}
