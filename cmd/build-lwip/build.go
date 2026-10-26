package main

import (
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

type manifest struct {
	ABI                int               `json:"abi"`
	Version            string            `json:"version"`
	Target             string            `json:"target"`
	Libc               string            `json:"libc,omitempty"`
	GOARM              string            `json:"goarm,omitempty"`
	Purego             bool              `json:"purego"`
	PuregoRequiresCgo  bool              `json:"purego_requires_cgo"`
	SourceRevision     string            `json:"source_revision,omitempty"`
	NativeSourceSHA256 string            `json:"native_source_sha256"`
	Compiler           string            `json:"compiler"`
	CC                 string            `json:"cc,omitempty"`
	CFlags             []string          `json:"cflags,omitempty"`
	LDFlags            []string          `json:"ldflags,omitempty"`
	Files              map[string]string `json:"files"`
}

func buildCommand(root string, args []string) error {
	flags := flag.NewFlagSet("build", flag.ContinueOnError)
	name := flags.String("target", "", "GOOS/GOARCH[/simulator]; tvos uses Go ios")
	libc := flags.String("libc", "gnu", "Linux libc label: gnu or musl (select a matching compiler/sysroot)")
	compiler := flags.String("cc", envDefault("CC", "cc"), "C compiler command, including target/sysroot options")
	archiver := flags.String("ar", envDefault("AR", "ar"), "archive tool command")
	destination := flags.String("output", "", "output directory")
	version := flags.String("version", "dev", "artifact version label")
	staticOnly := flags.Bool("static-only", false, "build only the static library (automatic for ios/tvos)")
	tests := flags.Bool("tests", false, "compile matching cgo and supported purego test executables")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	t, err := parseTarget(*name)
	if err != nil {
		return err
	}
	if *destination == "" || (*libc != "gnu" && *libc != "musl") {
		return fmt.Errorf("build requires --output DIR and --libc gnu|musl")
	}
	if *version == "" || strings.ContainsAny(*version, "/\\\x00") || *version == "." || *version == ".." {
		return fmt.Errorf("invalid version label %q", *version)
	}
	if (t.GOOS != runtime.GOOS || t.Arch != runtime.GOARCH) && *compiler == "cc" {
		return fmt.Errorf("cross builds require --cc or CC with the target toolchain/sysroot")
	}
	out, err := filepath.Abs(*destination)
	if err != nil {
		return err
	}
	if out == root {
		return fmt.Errorf("use a separate output directory, such as dist/%s", strings.ReplaceAll(t.Name, "/", "_"))
	}
	cc, err := commandWords(*compiler)
	if err != nil {
		return err
	}
	compilerVersion, err := compilerIdentity(root, cc)
	if err != nil {
		return err
	}
	ar, err := commandWords(*archiver)
	if err != nil {
		return err
	}
	cflags, err := splitWords(envDefault("CFLAGS", "-O3"))
	if err != nil {
		return err
	}
	cppflags, err := splitWords(os.Getenv("CPPFLAGS"))
	if err != nil {
		return err
	}
	cflags = append(cflags, cppflags...)
	if t.GOOS != "windows" {
		cflags = append(cflags, "-fPIC")
	}
	cflags = append(cflags, includes(root)...)
	cflags = append(cflags, "-ffile-prefix-map="+root+"=sing-lwip")
	ldflags, err := splitWords(os.Getenv("LDFLAGS"))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	directory, err := os.MkdirTemp(out, ".build-")
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
		object := filepath.Join(directory, strings.TrimSuffix(filepath.Base(source), ".c")+".o")
		command := append(append([]string(nil), cflags...), "-c", source, "-o", object)
		if err := run(root, nil, cc, command...); err != nil {
			return err
		}
		objects = append(objects, object)
	}
	if err := checkArch(objects[0], t.Arch); err != nil {
		return err
	}
	// Start with a new archive, so a rebuild cannot retain obsolete object members.
	static := filepath.Join(directory, "libsing-lwip.a")
	if err := run(root, nil, ar, append([]string{"rcs", static}, objects...)...); err != nil {
		return err
	}
	if err := copyFile(static, filepath.Join(out, "libsing-lwip.a")); err != nil {
		return err
	}
	payload := []string{"libsing-lwip.a", "bridge.h", "LICENSE", "LWIP-LICENSE"}
	*staticOnly = *staticOnly || t.GOOS == "ios"
	if !*staticOnly {
		sharedFlag := "-shared"
		if t.GOOS == "darwin" {
			sharedFlag = "-dynamiclib"
		}
		library := filepath.Join(out, t.library())
		command := append([]string{sharedFlag}, objects...)
		command = append(command, ldflags...)
		command = append(command, "-o", library)
		if err := run(root, nil, cc, command...); err != nil {
			return err
		}
		if err := checkArch(library, t.Arch); err != nil {
			return err
		}
		payload = append(payload, t.library())
		fixture := filepath.Join(out, "abi-fixture"+filepath.Ext(t.library()))
		command = append(append([]string(nil), cflags...), sharedFlag, filepath.Join(root, "tests/abi.c"))
		command = append(command, ldflags...)
		command = append(command, "-o", fixture)
		if err := run(root, nil, cc, command...); err != nil {
			return err
		}
		if err := checkArch(fixture, t.Arch); err != nil {
			return err
		}
	}
	for _, entry := range [][2]string{{"bridge.h", "bridge.h"}, {"LICENSE", "LICENSE"}, {"third_party/lwip/LICENSE", "LWIP-LICENSE"}} {
		if err := copyFile(filepath.Join(root, entry[0]), filepath.Join(out, entry[1])); err != nil {
			return err
		}
	}
	if *tests {
		if err := buildTests(root, out, *compiler, t, *staticOnly); err != nil {
			return err
		}
	}
	fingerprint, err := nativeFingerprint(root)
	if err != nil {
		return err
	}
	revision, _ := output(root, []string{"git"}, "rev-parse", "HEAD")
	meta := manifest{ABI: 2, Version: *version, Target: t.Name, Purego: t.purego() && !*staticOnly,
		PuregoRequiresCgo: t.GOOS == "android", SourceRevision: revision, NativeSourceSHA256: fingerprint,
		Compiler: strings.Split(compilerVersion, "\n")[0], Files: make(map[string]string)}
	if t.GOOS == "linux" {
		meta.Libc = *libc
	}
	if t.Arch == "arm" {
		meta.GOARM = envDefault("GOARM", "7")
	}
	for _, path := range payload {
		meta.Files[path], err = fileHash(filepath.Join(out, path))
		if err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "manifest.json"), append(data, '\n'), 0o644); err != nil {
		return err
	}
	payload = append(payload, "manifest.json")
	sort.Strings(payload)
	var sums strings.Builder
	for _, path := range payload {
		hash, err := fileHash(filepath.Join(out, path))
		if err != nil {
			return err
		}
		fmt.Fprintf(&sums, "%s  %s\n", hash, path)
	}
	if err := os.WriteFile(filepath.Join(out, "SHA256SUMS"), []byte(sums.String()), 0o644); err != nil {
		return err
	}
	payload = append(payload, "SHA256SUMS")
	archive := "sing-lwip-" + *version + "-abi2-" + strings.ReplaceAll(t.Name, "/", "-")
	if t.GOOS == "linux" {
		archive += "-" + *libc
	}
	if t.GOOS == "windows" {
		err = writeZip(out, archive+".zip", payload)
	} else {
		err = writeTar(out, archive+".tar.gz", payload)
	}
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

func buildTests(root, out, cc string, t target, staticOnly bool) error {
	for _, pure := range []bool{false, true} {
		if pure && (staticOnly || !t.purego()) {
			continue
		}
		env := map[string]string{"GOOS": t.GOOS, "GOARCH": t.Arch, "CC": cc, "CGO_ENABLED": "1"}
		name, suffix := "cgo", ".test"
		var tags []string
		if pure {
			name = "purego"
			tags = append(tags, "with_purego")
			if t.GOOS != "android" && t.GOOS != "ios" {
				env["CGO_ENABLED"] = "0"
			}
		}
		if t.GOOS == "windows" {
			suffix = ".exe"
		}
		if t.Platform == "tvos" {
			tags = append(tags, "tvos")
		}
		if t.Simulator {
			if t.Platform == "tvos" {
				tags = append(tags, "tvossimulator")
			} else {
				tags = append(tags, "iossimulator")
			}
		}
		// Go's cgo tool reads CGO_*FLAGS, whereas the native compiler uses *FLAGS.
		for _, key := range []string{"CFLAGS", "CPPFLAGS", "LDFLAGS"} {
			if value := os.Getenv(key); value != "" {
				env["CGO_"+key] = strings.TrimSpace(os.Getenv("CGO_"+key) + " " + value)
			}
		}
		command := []string{"test", "-c", "-tags", strings.Join(tags, ","), "-o", filepath.Join(out, name+suffix)}
		if pure && t.GOOS == "freebsd" {
			command = append(command, "-gcflags=github.com/ebitengine/purego/internal/fakecgo=-std")
		}
		if pure && t.GOOS == "linux" && t.Arch == "arm" {
			// Go's no-cgo ARM linker defaults to the soft-float loader name.
			// Our Linux ARM libraries use the armhf ABI.
			command = append(command, "-ldflags=-I=/lib/ld-linux-armhf.so.3")
		}
		if strings.HasPrefix(t.Arch, "mips") {
			command = append(command, "-ldflags=-w")
		}
		if err := run(root, env, []string{"go"}, append(command, ".")...); err != nil {
			return err
		}
	}
	return nil
}

func nativeFingerprint(root string) (string, error) {
	paths, err := nativeDependencies(root)
	if err != nil {
		return "", err
	}
	units, err := wrappers(root)
	if err != nil {
		return "", err
	}
	paths = append(paths, units...)
	for _, name := range []string{"bridge.c", "bridge.h", "lwipopts.h"} {
		paths = append(paths, filepath.Join(root, name))
	}
	sort.Slice(paths, func(i, j int) bool { return filepath.ToSlash(paths[i]) < filepath.ToSlash(paths[j]) })
	digest := sha256.New()
	for _, path := range paths {
		name, err := filepath.Rel(root, path)
		if err != nil {
			return "", err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		digest.Write([]byte(filepath.ToSlash(name) + "\x00"))
		digest.Write(data)
	}
	return fmt.Sprintf("%x", digest.Sum(nil)), nil
}
