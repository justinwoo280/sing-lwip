// Command build-lwip generates bindings, builds native libraries and runs C regressions.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	if err := runCLI(os.Args[1:]); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, "build-lwip:", err)
		os.Exit(1)
	}
}

func runCLI(args []string) error {
	flags := flag.NewFlagSet("build-lwip", flag.ContinueOnError)
	root := flags.String("root", "", "sing-lwip source directory (default: locate from current directory)")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), `Usage: go run ./cmd/build-lwip [--root DIR] <command> [options]

Commands:
  generate           Generate cgo compilation units and source fingerprints
  sync-lwip          Fetch or verify the locked lwIP fork commit
  build              Build and package a target's native libraries
  windows            Build an MSVC DLL using Clang/LLD and an xwin SDK
  zig-cc             Adapt Go's MIPS flags: <zig cc ...> -- <compiler arguments>
  matrix             Print a CI build matrix or the release target list
  publish            Validate artifacts and publish pinned native submodules
  test-native        Run native ASan/UBSan/LSan regressions
  test-upstream-udp  Verify the UDP ingress-netif fix before and after

Use <command> --help for command options.`)
	}
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() == 0 || flags.Arg(0) == "help" {
		flags.Usage()
		return nil
	}
	var err error
	*root, err = sourceRoot(*root)
	if err != nil {
		return err
	}
	switch flags.Arg(0) {
	case "generate":
		return generateCommand(*root, flags.Args()[1:])
	case "sync-lwip":
		return syncSourceCommand(*root, flags.Args()[1:])
	case "build":
		return buildCommand(*root, flags.Args()[1:])
	case "windows":
		return windowsCommand(*root, flags.Args()[1:])
	case "zig-cc":
		return zigCCCommand(flags.Args()[1:])
	case "matrix":
		return matrixCommand(flags.Args()[1:])
	case "publish":
		return publishCommand(*root, flags.Args()[1:])
	case "test-native":
		return testNativeCommand(*root, flags.Args()[1:])
	case "test-upstream-udp":
		return testUDPCommand(*root, flags.Args()[1:])
	default:
		return fmt.Errorf("unknown command %q", flags.Arg(0))
	}
}

func sourceRoot(path string) (string, error) {
	explicit := path != ""
	if !explicit {
		path = "."
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(path, "bridge.h")); err == nil {
			if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
				return path, nil
			}
		}
		parent := filepath.Dir(path)
		if explicit || parent == path {
			return "", fmt.Errorf("cannot locate sing-lwip sources; use --root")
		}
		path = parent
	}
}

func parseFlags(flags *flag.FlagSet, args []string) error {
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	return nil
}
