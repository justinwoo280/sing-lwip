//go:build (!cgo && !with_purego) || (with_purego && !((linux && (386 || amd64 || arm || arm64 || loong64 || ppc64le || riscv64)) || ((darwin || freebsd) && (amd64 || arm64)) || (windows && (386 || amd64 || arm64))))

package lwip

import (
	"fmt"
	"runtime"
)

func loadNative() (nativeAPI, error) {
	return nativeAPI{}, fmt.Errorf("lwIP on %s/%s requires cgo, or with_purego on a supported platform", runtime.GOOS, runtime.GOARCH)
}
