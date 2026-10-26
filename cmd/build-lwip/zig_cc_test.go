package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestZigCCCacheIsolation(t *testing.T) {
	if runtime.GOOS != "linux" || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		t.Skip("requires a Linux amd64/arm64 host")
	}
	zig, err := exec.LookPath("zig")
	if err != nil {
		t.Skip("zig unavailable")
	}
	root, err := sourceRoot("")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOWORK", "off")
	t.Setenv("GOFLAGS", "")
	for _, key := range []string{"CGO_CFLAGS", "CGO_CPPFLAGS", "CGO_CXXFLAGS", "CGO_LDFLAGS"} {
		t.Setenv(key, "")
	}
	directory := t.TempDir()
	// Exercise spaces in paths as well as the complete compiler argument list.
	wrapper := filepath.Join(directory, "build lwip")
	goTool := filepath.Join(runtime.GOROOT(), "bin/go")
	if _, err := output(root, []string{goTool}, "build", "-o", wrapper, "./cmd/build-lwip"); err != nil {
		t.Fatal(err)
	}
	for name, contents := range map[string]string{
		"go.mod": "module example.com/zig-cache\n\ngo 1.24.0\n",
		"main.go": `package main

/*
#include <features.h>
static int libc_id(void) {
#ifdef __GLIBC__
    return 1;
#else
    return 2;
#endif
}
*/
import "C"
import "fmt"

func main() { fmt.Println(int(C.libc_id())) }
`,
	} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cpu := "x86_64"
	if runtime.GOARCH == "arm64" {
		cpu = "aarch64"
	}
	env := map[string]string{
		"GOOS": runtime.GOOS, "GOARCH": runtime.GOARCH, "CGO_ENABLED": "1",
		"GOCACHE": filepath.Join(directory, "go-cache"), "GOPROXY": "off", "GOSUMDB": "off",
	}
	binary := filepath.Join(directory, "consumer")
	// One stable wrapper and one cache must work in both switching directions.
	// A wrapper hiding its target in an environment variable links cached CGO
	// objects compiled against the wrong libc headers, even if linking succeeds.
	for _, abi := range []string{"musl", "gnu.2.17", "musl", "gnu.2.17"} {
		cc := fmt.Sprintf("%q cc -target %s-linux-%s", zig, cpu, abi)
		env["CC"] = zigCCWrapper(wrapper, root, cc)
		if _, err := outputEnv(directory, env, []string{goTool}, "build", "-o", binary, "."); err != nil {
			t.Fatalf("%s: %v", abi, err)
		}
		want := "1"
		if abi == "musl" {
			want = "2"
		}
		if got, err := output(directory, []string{binary}); err != nil || got != want {
			t.Fatalf("%s: libc ID = %q, want %q: %v", abi, got, want, err)
		}
	}
}
