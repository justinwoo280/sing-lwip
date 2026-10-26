package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func isZigCC(command []string) bool {
	for i := 0; i+1 < len(command); i++ {
		if (filepath.Base(command[i]) == "zig" || filepath.Base(command[i]) == "zig.exe") && command[i+1] == "cc" {
			return true
		}
	}
	return false
}

func zigCCCommand(args []string) error {
	compiler, err := commandWords(os.Getenv("SING_LWIP_CC"))
	if err != nil {
		return err
	}
	if !isZigCC(compiler) {
		return fmt.Errorf("SING_LWIP_CC must select zig cc")
	}
	var filtered []string
	for _, arg := range args {
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
