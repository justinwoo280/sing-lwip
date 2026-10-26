package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func windowsCommand(root string, args []string) error {
	flags := flag.NewFlagSet("windows", flag.ContinueOnError)
	sdk := flags.String("sdk", "toolchains/xwin", "xwin MSVC CRT and Windows SDK splat directory")
	arch := flags.String("arch", "", "target architecture: amd64 or arm64")
	destination := flags.String("output", "", "output DLL path")
	clang := flags.String("clang", "clang", "Clang executable")
	linker := flags.String("linker", "lld-link", "LLD linker executable")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if (*arch != "amd64" && *arch != "arm64") || *destination == "" {
		return fmt.Errorf("windows requires --arch amd64|arm64 and --output PATH")
	}
	sdkPath, err := filepath.Abs(*sdk)
	if err != nil {
		return err
	}
	outputPath, err := filepath.Abs(*destination)
	if err != nil {
		return err
	}
	cpu, machine := "x86_64", "x64"
	if *arch == "arm64" {
		cpu, machine = "aarch64", "arm64"
	}
	include := []string{root, filepath.Join(root, "third_party/lwip/src/include"), filepath.Join(root, "third_party/lwip/src/ports/include"),
		filepath.Join(sdkPath, "crt/include"), filepath.Join(sdkPath, "sdk/include/ucrt"), filepath.Join(sdkPath, "sdk/include/shared"), filepath.Join(sdkPath, "sdk/include/um")}
	libraries := []string{filepath.Join(sdkPath, "crt/lib", cpu), filepath.Join(sdkPath, "sdk/lib/ucrt", cpu), filepath.Join(sdkPath, "sdk/lib/um", cpu)}
	for _, path := range append(append([]string(nil), include...), libraries...) {
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			return fmt.Errorf("missing SDK/include directory: %s", path)
		}
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return err
	}
	directory, err := os.MkdirTemp("", "sing-lwip-msvc-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	sources, err := wrappers(root)
	if err != nil {
		return err
	}
	sources = append(sources, filepath.Join(root, "bridge.c"))
	var objects []string
	for _, source := range sources {
		obj := filepath.Join(directory, strings.TrimSuffix(filepath.Base(source), ".c")+".obj")
		command := []string{"--driver-mode=cl", "--target=" + cpu + "-pc-windows-msvc", "/nologo", "/O2", "/MT", "/std:c11", "/c"}
		command = append(command, "/clang:-ffile-prefix-map="+root+"=sing-lwip")
		for _, path := range include {
			command = append(command, "/I"+path)
		}
		command = append(command, source, "/Fo"+obj)
		if err := run(root, nil, []string{*clang}, command...); err != nil {
			return err
		}
		objects = append(objects, obj)
	}
	command := []string{"/dll", "/machine:" + machine, "/out:" + outputPath, "/implib:" + filepath.Join(directory, "sing-lwip.lib")}
	for _, path := range libraries {
		command = append(command, "/libpath:"+path)
	}
	command = append(command, objects...)
	command = append(command, "kernel32.lib")
	if err := run(root, nil, []string{*linker}, command...); err != nil {
		return err
	}
	if err := checkArch(outputPath, *arch); err != nil {
		return err
	}
	fmt.Println(outputPath)
	return nil
}
