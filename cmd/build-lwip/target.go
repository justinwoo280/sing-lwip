package main

import (
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"encoding/binary"
	"fmt"
	"os"
	"strings"
)

type target struct {
	Name, Platform, GOOS, Arch string
	Simulator                  bool
}

func parseTarget(name string) (target, error) {
	parts := strings.Split(name, "/")
	if len(parts) < 2 || len(parts) > 3 || (len(parts) == 3 && parts[2] != "simulator") {
		return target{}, fmt.Errorf("target must be GOOS/GOARCH[/simulator]")
	}
	t := target{Name: name, Platform: parts[0], GOOS: parts[0], Arch: parts[1], Simulator: len(parts) == 3}
	if t.Platform == "tvos" {
		t.GOOS = "ios"
	}
	allowed := map[string]string{
		"linux":   "386 amd64 arm arm64 loong64 mips mipsle mips64 mips64le ppc64le riscv64 s390x",
		"android": "386 amd64 arm arm64", "darwin": "amd64 arm64", "ios": "amd64 arm64",
		"tvos": "amd64 arm64", "freebsd": "amd64 arm64", "windows": "386 amd64 arm64",
	}
	if !strings.Contains(" "+allowed[t.Platform]+" ", " "+t.Arch+" ") || t.Arch == "" {
		return target{}, fmt.Errorf("unsupported target %q", name)
	}
	if t.Simulator && t.GOOS != "ios" {
		return target{}, fmt.Errorf("simulator is only valid for ios/tvos")
	}
	return t, nil
}

func (t target) purego() bool {
	switch t.GOOS {
	case "linux", "android":
		return strings.Contains(" 386 amd64 arm arm64 loong64 ppc64le riscv64 ", " "+t.Arch+" ")
	case "darwin", "ios", "freebsd":
		return t.Arch == "amd64" || t.Arch == "arm64"
	case "windows":
		return t.Arch == "386" || t.Arch == "amd64" || t.Arch == "arm64"
	}
	return false
}

func (t target) library() string {
	switch t.GOOS {
	case "windows":
		return "sing-lwip.dll"
	case "darwin", "ios":
		return "libsing-lwip.dylib"
	default:
		return "libsing-lwip.so"
	}
}

// Go's no-cgo linker defaults differ from these published glibc ABIs.
func (t target) puregoInterpreter() string {
	if t.GOOS == "linux" {
		switch t.Arch {
		case "arm":
			return "/lib/ld-linux-armhf.so.3"
		case "riscv64":
			return "/lib/ld-linux-riscv64-lp64d.so.1"
		}
	}
	return ""
}

func checkArch(path, arch string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var magic [4]byte
	if _, err := f.ReadAt(magic[:], 0); err != nil {
		return err
	}
	if string(magic[:]) == "\x7fELF" {
		object, err := elf.NewFile(f)
		if err != nil {
			return err
		}
		machines := map[string]elf.Machine{"386": elf.EM_386, "amd64": elf.EM_X86_64, "arm": elf.EM_ARM, "arm64": elf.EM_AARCH64,
			"mips": elf.EM_MIPS, "mipsle": elf.EM_MIPS, "mips64": elf.EM_MIPS, "mips64le": elf.EM_MIPS,
			"ppc64le": elf.EM_PPC64, "riscv64": elf.EM_RISCV, "loong64": elf.EM_LOONGARCH, "s390x": elf.EM_S390}
		class := elf.ELFCLASS64
		if arch == "386" || arch == "arm" || arch == "mips" || arch == "mipsle" {
			class = elf.ELFCLASS32
		}
		order := elf.ELFDATA2LSB
		if arch == "mips" || arch == "mips64" || arch == "s390x" {
			order = elf.ELFDATA2MSB
		}
		if object.Machine == machines[arch] && object.Class == class && object.Data == order {
			return nil
		}
		return fmt.Errorf("%s: expected %s, found ELF %s/%s/%s", path, arch, object.Machine, object.Class, object.Data)
	}
	if value := binary.LittleEndian.Uint32(magic[:]); value == macho.Magic64 || value == macho.Magic32 {
		object, err := macho.NewFile(f)
		if err != nil {
			return err
		}
		cpu := map[string]macho.Cpu{"amd64": macho.CpuAmd64, "arm64": macho.CpuArm64}[arch]
		if cpu != 0 && object.Cpu == cpu {
			return nil
		}
		return fmt.Errorf("%s: expected %s, found Mach-O %s", path, arch, object.Cpu)
	}
	object, err := pe.NewFile(f)
	if err != nil {
		return fmt.Errorf("%s: unrecognized native object format: %w", path, err)
	}
	machine := map[string]uint16{"386": pe.IMAGE_FILE_MACHINE_I386, "amd64": pe.IMAGE_FILE_MACHINE_AMD64, "arm64": pe.IMAGE_FILE_MACHINE_ARM64}[arch]
	if machine == 0 || object.Machine != machine {
		return fmt.Errorf("%s: expected %s, found PE/COFF machine %#x", path, arch, object.Machine)
	}
	return nil
}
