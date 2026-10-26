package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

func isZigCC(command []string) bool {
	for i := 0; i+1 < len(command); i++ {
		if (filepath.Base(command[i]) == "zig" || filepath.Base(command[i]) == "zig.exe") && command[i+1] == "cc" {
			return true
		}
	}
	return false
}

func zigCCWrapper(executable, root, cc string) string {
	// Go hashes CC, but not arbitrary environment variables read by a wrapper.
	// Keep the compiler and all target/sysroot flags visible in CC so glibc and
	// musl never reuse each other's cached CGO objects (including runtime/cgo).
	return fmt.Sprintf("%q --root %q zig-cc %s --", executable, root, cc)
}

func zigCCCommand(args []string) error {
	separator := slices.Index(args, "--")
	if separator < 0 {
		return fmt.Errorf("zig-cc requires <zig cc ...> -- <compiler arguments>")
	}
	compiler := args[:separator]
	if !isZigCC(compiler) {
		return fmt.Errorf("zig-cc compiler must select zig cc")
	}
	var filtered []string
	for _, arg := range args[separator+1:] {
		// Go emits -mhard-float for MIPS. Zig treats it as a nonexistent LLVM
		// feature; the selected gnueabihf/gnuabi64/musleabihf target already
		// specifies hard-float. Other flags, including -msoft-float, pass through.
		if arg != "-mhard-float" {
			filtered = append(filtered, arg)
		}
	}
	cmd := process("", nil, compiler, filtered...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}
