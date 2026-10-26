//go:build with_purego && ((linux && (386 || amd64 || arm || arm64 || loong64 || ppc64le || riscv64)) || ((darwin || freebsd) && (amd64 || arm64)) || (windows && (386 || amd64 || arm64)))

package lwip

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/ebitengine/purego"
)

func loadNative() (nativeAPI, error) {
	path := os.Getenv("SING_LWIP_LIBRARY")
	if path == "" {
		exe, err := os.Executable()
		if err != nil {
			return nativeAPI{}, err
		}
		name := "libsing-lwip.so"
		if runtime.GOOS == "darwin" || runtime.GOOS == "ios" {
			name = "libsing-lwip.dylib"
		} else if runtime.GOOS == "windows" {
			name = "sing-lwip.dll"
		}
		path = filepath.Join(filepath.Dir(exe), name)
	}
	h, err := openLibrary(path)
	if err != nil {
		return nativeAPI{}, fmt.Errorf("load sing-lwip for %s/%s from %q: %w", runtime.GOOS, runtime.GOARCH, path, err)
	}
	var f [11]uintptr
	for i, name := range []string{"sl_abi", "sl_create", "sl_destroy", "sl_input", "sl_tick", "sl_drain", "sl_output", "sl_read", "sl_write", "sl_shutdown", "sl_close"} {
		f[i], err = librarySymbol(h, name)
		if err != nil {
			closeLibrary(h)
			return nativeAPI{}, err
		}
	}
	version, _, _ := purego.SyscallN(f[0])
	if uint32(version) != nativeABIVersion {
		closeLibrary(h)
		return nativeAPI{}, fmt.Errorf("unsupported sing-lwip ABI: %d (need %d)", uint32(version), nativeABIVersion)
	}
	// The process core caches this binding. Do not unload code while a PCB or
	// timer can reference its callbacks. The ABI deliberately has no floats or structs by value.
	return bindNative(f), nil
}
