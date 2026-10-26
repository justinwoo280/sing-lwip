package lwip

import "unsafe"

const nativeABIVersion = 2

type nativeAPI struct {
	abi      func() uint32
	create   func(unsafe.Pointer) uint64
	destroy  func(uint64)
	input    func(uint64, unsafe.Pointer, uint32) int32
	tick     func()
	drain    func(uint64, unsafe.Pointer, uint32) uint32
	output   func(uint64, unsafe.Pointer, uint32) uint32
	read     func(uint64, unsafe.Pointer, uint32, unsafe.Pointer) int32
	write    func(uint64, unsafe.Pointer, uint32, unsafe.Pointer) int32
	shutdown func(uint64, uint32, uint32) int32
	close    func(uint64, uint32)
}
