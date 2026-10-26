//go:build with_purego && ((linux && (386 || amd64 || arm || arm64 || loong64 || ppc64le || riscv64)) || ((darwin || freebsd) && (amd64 || arm64)) || (windows && (386 || amd64 || arm64)))

package lwip

import (
	"os"
	"runtime"
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"
)

func TestPuregoHandleABI(t *testing.T) {
	path := os.Getenv("SING_LWIP_ABI_FIXTURE")
	if path == "" {
		t.Skip("set SING_LWIP_ABI_FIXTURE to the tests/abi.c shared library")
	}
	h, err := openLibrary(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeLibrary(h)
	var f [11]uintptr
	for i, name := range []string{"sl_abi", "sl_create", "sl_destroy", "sl_input", "sl_tick", "sl_drain", "sl_output", "sl_read", "sl_write", "sl_shutdown", "sl_close"} {
		f[i], err = librarySymbol(h, name)
		if err != nil {
			t.Fatal(err)
		}
	}
	a := bindNative(f)
	data, address := [17]byte{0x42}, [40]byte{}
	p, addr := unsafe.Pointer(&data[0]), unsafe.Pointer(&address[0])
	id := a.create(p)
	if a.abi() != 2 || id != 0xa123456789abcdef {
		t.Fatalf("ABI/uint64 return mismatch: id=%x", id)
	}
	if n := a.input(id, p, 17); n != -123 {
		t.Fatalf("input ABI: %d", n)
	}
	if a.drain(id, p, 17) != 17 || data[0] != 0x51 || a.output(id, p, 17) != 17 {
		t.Fatal("output pointer/length ABI")
	}
	if a.read(id, p, 17, addr) != 17 || data[0] != 0x61 || address[0] != 0x71 || a.write(id, p, 17, addr) != 17 {
		t.Fatal("read/write stack argument ABI")
	}
	if a.shutdown(id, 0x1234, 0x5678) != -1234567 {
		t.Fatal("shutdown ABI")
	}
	a.tick()
	a.close(id, 0x9abc)
	a.destroy(id)
	status, err := librarySymbol(h, "sl_fixture_status")
	if err != nil {
		t.Fatal(err)
	}
	seen, _, _ := purego.SyscallN(status)
	if seen != 511 {
		t.Fatalf("missing ABI calls: %x", seen)
	}
	runtime.KeepAlive(data)
	runtime.KeepAlive(address)
}
