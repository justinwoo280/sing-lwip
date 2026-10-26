//go:build with_purego && (((linux || darwin || freebsd || windows) && (amd64 || arm64)) || (linux && (loong64 || ppc64le || riscv64)))

package lwip

import (
	"unsafe"

	"github.com/ebitengine/purego"
)

func bindNative(f [11]uintptr) nativeAPI {
	return nativeAPI{
		abi:     func() uint32 { r, _, _ := purego.SyscallN(f[0]); return uint32(r) },
		create:  func(p unsafe.Pointer) uint64 { r, _, _ := purego.SyscallN(f[1], uintptr(p)); return uint64(r) },
		destroy: func(id uint64) { purego.SyscallN(f[2], uintptr(id)) },
		input: func(id uint64, p unsafe.Pointer, n uint32) int32 {
			r, _, _ := purego.SyscallN(f[3], uintptr(id), uintptr(p), uintptr(n))
			return int32(r)
		},
		tick: func() { purego.SyscallN(f[4]) },
		drain: func(id uint64, p unsafe.Pointer, n uint32) uint32 {
			r, _, _ := purego.SyscallN(f[5], uintptr(id), uintptr(p), uintptr(n))
			return uint32(r)
		},
		output: func(id uint64, p unsafe.Pointer, n uint32) uint32 {
			r, _, _ := purego.SyscallN(f[6], uintptr(id), uintptr(p), uintptr(n))
			return uint32(r)
		},
		read: func(id uint64, p unsafe.Pointer, n uint32, a unsafe.Pointer) int32 {
			r, _, _ := purego.SyscallN(f[7], uintptr(id), uintptr(p), uintptr(n), uintptr(a))
			return int32(r)
		},
		write: func(id uint64, p unsafe.Pointer, n uint32, a unsafe.Pointer) int32 {
			r, _, _ := purego.SyscallN(f[8], uintptr(id), uintptr(p), uintptr(n), uintptr(a))
			return int32(r)
		},
		shutdown: func(id uint64, r, w uint32) int32 {
			n, _, _ := purego.SyscallN(f[9], uintptr(id), uintptr(r), uintptr(w))
			return int32(n)
		},
		close: func(id uint64, abort uint32) { purego.SyscallN(f[10], uintptr(id), uintptr(abort)) },
	}
}
