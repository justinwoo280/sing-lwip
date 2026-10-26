//go:build with_purego && ((linux && (386 || amd64 || arm || arm64 || loong64 || ppc64le || riscv64)) || ((darwin || freebsd) && (amd64 || arm64)))

package lwip

import "github.com/ebitengine/purego"

func openLibrary(path string) (uintptr, error) {
	return purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_LOCAL)
}
func librarySymbol(h uintptr, name string) (uintptr, error) { return purego.Dlsym(h, name) }
func closeLibrary(h uintptr)                                { _ = purego.Dlclose(h) }
