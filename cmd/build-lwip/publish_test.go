package main

import (
	"encoding/json"
	"go/build/constraint"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPublishedPlatformSelection(t *testing.T) {
	cases := []struct {
		want string
		tags string
	}{
		{"linux_arm64_gnu", "linux arm64"},
		{"linux_arm64_musl", "linux arm64 with_musl"},
		{"linux_arm_gnu", "linux arm arm.5 arm.6 arm.7"},
		{"android_arm64", "android linux arm64"},
		{"darwin_arm64", "darwin arm64"},
		{"ios_arm64", "ios darwin arm64"},
		{"ios_arm64_simulator", "ios darwin arm64 iossimulator"},
		{"tvos_arm64", "ios darwin arm64 tvos"},
		{"tvos_amd64_simulator", "ios darwin amd64 tvos tvossimulator"},
		{"windows_386", "windows 386"},
		{"freebsd_arm64", "freebsd arm64"},
	}
	for _, test := range cases {
		t.Run(test.want, func(t *testing.T) {
			var selected []string
			for _, id := range releaseIDs() {
				parts := strings.Split(id, "_")
				name, libc := parts[0]+"/"+parts[1], ""
				if len(parts) == 3 {
					if parts[2] == "simulator" {
						name += "/simulator"
					} else {
						libc = parts[2]
					}
				}
				target, err := parseTarget(name)
				if err != nil {
					t.Fatal(err)
				}
				expression, err := constraint.Parse("//go:build " + target.constraint(libc))
				if err != nil {
					t.Fatal(err)
				}
				if expression.Eval(func(tag string) bool { return strings.Contains(" "+test.tags+" ", " "+tag+" ") }) {
					selected = append(selected, id)
				}
			}
			if len(selected) != 1 || selected[0] != test.want {
				t.Fatalf("selected %v for %q", selected, test.tags)
			}
		})
	}
}

func TestPublishPinnedGraphAndHistory(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	t.Setenv("GOWORK", "off")
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOSUMDB", "off")
	root, remote, artifacts := t.TempDir(), t.TempDir(), t.TempDir()
	write := func(path, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git := func(directory string, args ...string) string {
		t.Helper()
		text, err := output(directory, []string{"git"}, args...)
		if err != nil {
			t.Fatal(err)
		}
		return text
	}
	git(remote, "init", "--bare")
	git(root, "init", "-b", "main")
	git(root, "config", "user.name", "Publisher Test")
	git(root, "config", "user.email", "publisher@example.invalid")
	git(root, "remote", "add", "origin", remote)
	write(filepath.Join(root, "go.mod"), "module example.com/sing-lwip\n\ngo 1.24.0\n")
	write(filepath.Join(root, "lwip.go"), "package lwip\n")
	write(filepath.Join(root, ".gitignore"), "/lib/\n/prebuilt_*.go\n")
	for _, name := range []string{"bridge.c", "lwip_core.c"} {
		write(filepath.Join(root, name), "//go:build cgo && !with_lwip_prebuilt\n\n/* fixture */\n")
	}
	for _, name := range []string{"bridge.h", "lwipopts.h", "checksum.h", "checksum_generated.h", "third_party/lwip/src/fixture.h", "LICENSE", "third_party/lwip/LICENSE"} {
		write(filepath.Join(root, name), "/* fixture */\n")
	}
	snapshot, err := readSourceSnapshot(filepath.Join(root, "third_party/lwip"))
	if err != nil {
		t.Fatal(err)
	}
	source := sourceLock{"https://github.com/example/lwip.git", strings.Repeat("a", 40), sourceTreeHash(snapshot)}
	lockData, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(root, sourceLockFile), string(lockData))
	git(root, "add", ".")
	git(root, "commit", "-m", "source")
	id := "linux_" + runtime.GOARCH + "_gnu"
	// No native code is executed here; a normal, empty ar archive exercises module transport.
	var previous string
	for iteration := 0; iteration < 2; iteration++ {
		if iteration > 0 {
			write(filepath.Join(root, "README.md"), "second source revision\n")
			git(root, "add", "README.md")
			git(root, "commit", "-m", "update source")
		}
		git(root, "push", "origin", "main")
		head := git(root, "rev-parse", "HEAD")
		fingerprint, err := nativeFingerprint(root)
		if err != nil {
			t.Fatal(err)
		}
		meta := manifest{ABI: 2, Target: "linux/" + runtime.GOARCH, Libc: "gnu", SourceRevision: head, LWIPSource: source, NativeSourceSHA256: fingerprint, Files: map[string]string{}}
		write(filepath.Join(artifacts, "libsing-lwip.a"), "!<arch>\n")
		for _, pair := range [][2]string{{"bridge.h", "bridge.h"}, {"LICENSE", "LICENSE"}, {"third_party/lwip/LICENSE", "LWIP-LICENSE"}} {
			if err := copyFile(filepath.Join(root, pair[0]), filepath.Join(artifacts, pair[1])); err != nil {
				t.Fatal(err)
			}
		}
		for _, name := range []string{"libsing-lwip.a", "bridge.h", "LICENSE", "LWIP-LICENSE"} {
			meta.Files[name], err = fileHash(filepath.Join(artifacts, name))
			if err != nil {
				t.Fatal(err)
			}
		}
		data, _ := json.Marshal(meta)
		write(filepath.Join(artifacts, "manifest.json"), string(data))
		if err := publishCommand(root, []string{"--artifacts", artifacts, "--expected", id, "--source-branch", "main"}); err != nil {
			t.Fatal(err)
		}
		published := strings.Fields(git(root, "ls-remote", "origin", "refs/heads/go"))[0]
		mod := git(root, "show", published+":go.mod")
		if strings.Contains(mod, "replace") || !strings.Contains(mod, "example.com/sing-lwip/lib/"+id+" v0.0.0-") {
			t.Fatalf("unpublished or unpinned dependency in go.mod: %s", mod)
		}
		var index struct {
			LWIPSource sourceLock `json:"lwip_source"`
		}
		if err := json.Unmarshal([]byte(git(root, "show", published+":native-build.json")), &index); err != nil || index.LWIPSource != source {
			t.Fatalf("published lwIP provenance mismatch: %v, %v", index, err)
		}
		if previous != "" {
			git(root, "merge-base", "--is-ancestor", previous, published)
		}
		if git(root, "rev-parse", "HEAD") != head || git(root, "status", "--porcelain") != "" {
			t.Fatal("publisher changed source worktree")
		}
		// A different lwIP commit must be rejected even with matching binary hashes.
		wrongSource := meta
		wrongSource.LWIPSource.Revision = strings.Repeat("b", 40)
		wrongData, _ := json.Marshal(wrongSource)
		write(filepath.Join(artifacts, "manifest.json"), string(wrongData))
		if err := publishCommand(root, []string{"--artifacts", artifacts, "--expected", id}); err == nil {
			t.Fatal("accepted a different lwIP source revision")
		}
		write(filepath.Join(artifacts, "manifest.json"), string(data))
		// Corrupt artifacts must be rejected without advancing the published ref.
		write(filepath.Join(artifacts, "libsing-lwip.a"), "corrupt")
		if err := publishCommand(root, []string{"--artifacts", artifacts, "--expected", id}); err == nil {
			t.Fatal("accepted corrupt native library")
		}
		if strings.Fields(git(root, "ls-remote", "origin", "refs/heads/go"))[0] != published {
			t.Fatal("invalid artifacts advanced remote branch")
		}
		previous = published
	}
}
