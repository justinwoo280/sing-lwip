package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
)

// One catalog drives CI and the publisher's completeness check.
func buildMatrices() map[string][]map[string]string {
	linux := []map[string]string{
		{"arch": "amd64", "cpu": "x86_64", "gnu": "gnu.2.17", "runner": "ubuntu-24.04", "qemu": "", "sysroot": "", "packages": "", "musl": "true"},
		{"arch": "arm64", "cpu": "aarch64", "gnu": "gnu.2.17", "runner": "ubuntu-24.04-arm", "qemu": "", "sysroot": "", "packages": "", "musl": "true"},
		{"arch": "386", "cpu": "x86", "gnu": "gnu.2.17", "runner": "ubuntu-24.04", "qemu": "qemu-i386", "sysroot": "/", "packages": "libc6-i386 qemu-user", "musl": "true"},
		{"arch": "arm", "cpu": "arm", "gnu": "gnueabihf.2.17", "runner": "ubuntu-24.04", "qemu": "qemu-arm", "sysroot": "/usr/arm-linux-gnueabihf", "packages": "libc6-armhf-cross qemu-user", "musl": "true"},
		{"arch": "riscv64", "cpu": "riscv64", "gnu": "gnu.2.27", "runner": "ubuntu-24.04", "qemu": "qemu-riscv64", "sysroot": "/usr/riscv64-linux-gnu", "packages": "libc6-riscv64-cross qemu-user", "musl": "true"},
		{"arch": "ppc64le", "cpu": "powerpc64le", "gnu": "gnu.2.19", "runner": "ubuntu-24.04", "qemu": "qemu-ppc64le", "sysroot": "/usr/powerpc64le-linux-gnu", "packages": "libc6-ppc64el-cross qemu-user", "musl": "true"},
		{"arch": "loong64", "cpu": "loongarch64", "gnu": "gnu.2.36", "runner": "ubuntu-24.04", "qemu": "qemu-loongarch64", "sysroot": "", "packages": "qemu-user", "musl": "true"},
		{"arch": "mipsle", "cpu": "mipsel", "gnu": "gnueabihf.2.17", "runner": "ubuntu-24.04", "qemu": "qemu-mipsel", "sysroot": "/usr/mipsel-linux-gnu", "packages": "libc6-mipsel-cross qemu-user", "musl": "true"},
		{"arch": "mips64le", "cpu": "mips64el", "gnu": "gnuabi64.2.17", "runner": "ubuntu-24.04", "qemu": "qemu-mips64el", "sysroot": "/usr/mips64el-linux-gnuabi64", "packages": "libc6-mips64el-cross qemu-user", "musl": "false"},
	}
	return map[string][]map[string]string{
		"linux": linux,
		"windows": {
			{"arch": "386", "cpu": "x86", "runner": "windows-2022"},
			{"arch": "amd64", "cpu": "x86_64", "runner": "windows-2022"},
			{"arch": "arm64", "cpu": "aarch64", "runner": "windows-11-arm"},
		},
		"apple": {
			{"target": "darwin/amd64", "runner": "macos-15-intel", "sdk": "macosx", "triple": "x86_64-apple-macos12.0", "runtime": "true"},
			{"target": "darwin/arm64", "runner": "macos-15", "sdk": "macosx", "triple": "arm64-apple-macos12.0", "runtime": "true"},
			{"target": "ios/arm64", "runner": "macos-15", "sdk": "iphoneos", "triple": "arm64-apple-ios15.0", "runtime": "false"},
			{"target": "ios/arm64/simulator", "runner": "macos-15", "sdk": "iphonesimulator", "triple": "arm64-apple-ios15.0-simulator", "runtime": "false"},
			{"target": "ios/amd64/simulator", "runner": "macos-15", "sdk": "iphonesimulator", "triple": "x86_64-apple-ios15.0-simulator", "runtime": "false"},
			{"target": "tvos/arm64", "runner": "macos-15", "sdk": "appletvos", "triple": "arm64-apple-tvos15.0", "runtime": "false"},
			{"target": "tvos/arm64/simulator", "runner": "macos-15", "sdk": "appletvsimulator", "triple": "arm64-apple-tvos15.0-simulator", "runtime": "false"},
			{"target": "tvos/amd64/simulator", "runner": "macos-15", "sdk": "appletvsimulator", "triple": "x86_64-apple-tvos15.0-simulator", "runtime": "false"},
		},
		"android": {
			{"arch": "386", "cc": "i686-linux-android23-clang"},
			{"arch": "amd64", "cc": "x86_64-linux-android23-clang"},
			{"arch": "arm", "cc": "armv7a-linux-androideabi23-clang"},
			{"arch": "arm64", "cc": "aarch64-linux-android23-clang"},
		},
		"freebsd": {{"arch": "amd64", "cpu": "x86_64"}, {"arch": "arm64", "cpu": "aarch64"}},
	}
}

func releaseIDs() []string {
	var result []string
	for group, entries := range buildMatrices() {
		for _, entry := range entries {
			if group == "linux" {
				result = append(result, "linux_"+entry["arch"]+"_gnu")
				if entry["musl"] == "true" {
					result = append(result, "linux_"+entry["arch"]+"_musl")
				}
			} else if group == "apple" {
				result = append(result, strings.ReplaceAll(entry["target"], "/", "_"))
			} else {
				result = append(result, group+"_"+entry["arch"])
			}
		}
	}
	sort.Strings(result)
	return result
}

func matrixCommand(args []string) error {
	flags := flag.NewFlagSet("matrix", flag.ContinueOnError)
	group := flags.String("group", "", "linux, windows, apple, android, freebsd, or targets")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if *group == "targets" {
		return json.NewEncoder(os.Stdout).Encode(releaseIDs())
	}
	entries, ok := buildMatrices()[*group]
	if !ok {
		return fmt.Errorf("unknown matrix group %q", *group)
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"include": entries})
}

func (t target) libraryID(libc string) string {
	id := strings.ReplaceAll(t.Name, "/", "_")
	if t.GOOS == "linux" {
		id += "_" + libc
	}
	return id
}

func (t target) constraint(libc string) string {
	parts := []string{t.GOOS, t.Arch}
	switch t.GOOS {
	case "linux":
		parts = append(parts, "!android")
		if libc == "musl" {
			parts = append(parts, "with_musl")
		} else {
			parts = append(parts, "!with_musl")
		}
	case "darwin":
		parts = append(parts, "!ios")
	case "ios":
		tv, simulator := "!tvos", "iossimulator"
		if t.Platform == "tvos" {
			tv, simulator = "tvos", "tvossimulator"
		}
		if !t.Simulator {
			simulator = "!" + simulator
		}
		parts = append(parts, tv, simulator)
	}
	if t.Arch == "arm" {
		parts = append(parts, "arm.7")
	}
	return strings.Join(parts, " && ")
}
