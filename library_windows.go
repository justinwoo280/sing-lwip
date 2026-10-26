//go:build with_purego && (386 || amd64 || arm64)

package lwip

import "syscall"

func openLibrary(path string) (uintptr, error) {
	h, e := syscall.LoadLibrary(path)
	return uintptr(h), e
}
func librarySymbol(h uintptr, name string) (uintptr, error) {
	return syscall.GetProcAddress(syscall.Handle(h), name)
}
func closeLibrary(h uintptr) { _ = syscall.FreeLibrary(syscall.Handle(h)) }
