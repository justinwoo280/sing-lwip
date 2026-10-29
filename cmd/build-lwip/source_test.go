package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareSourceCache(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	repository := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		text, err := output(repository, []string{"git"}, args...)
		if err != nil {
			t.Fatal(err)
		}
		return text
	}
	write := func(name, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-b", "main")
	git("config", "user.name", "Source Test")
	git("config", "user.email", "source@example.invalid")
	write(filepath.Join(repository, "LICENSE"), "test license\n")
	write(filepath.Join(repository, "src/core.c"), "old source\n")
	git("add", ".")
	git("commit", "-m", "pinned source")
	first := git("rev-parse", "HEAD")
	snapshot, err := fetchSourceSnapshot(repository, first)
	if err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(repository, "src/core.c"), "new branch head\n")
	git("commit", "-am", "advance branch")
	second := git("rev-parse", "HEAD")
	const remote = "https://github.com/example/lwip.git"
	// Exercise the real Git downloader without relying on public network access.
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url."+filepath.ToSlash(repository)+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", remote)
	lock := sourceLock{remote, first, sourceTreeHash(snapshot)}
	prepareRoot := func(t *testing.T, lock sourceLock) (string, []byte) {
		t.Helper()
		root := t.TempDir()
		// Noncanonical whitespace detects accidental lock rewrites during a build.
		data, err := json.Marshal(lock)
		if err != nil {
			t.Fatal(err)
		}
		data = append(data, '\n', '\n')
		write(filepath.Join(root, sourceLockFile), string(data))
		write(filepath.Join(root, "checksum_generated.h"), "generated sentinel\n")
		return root, data
	}
	assertLock := func(root string, want []byte) {
		t.Helper()
		got, err := os.ReadFile(filepath.Join(root, sourceLockFile))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("lock changed: %v", err)
		}
	}
	t.Run("exact-pin-and-offline-reuse", func(t *testing.T) {
		root, original := prepareRoot(t, lock)
		if got, err := ensureLWIPSource(root); err != nil || got != lock {
			t.Fatalf("prepare: %v, %v", got, err)
		}
		file := filepath.Join(root, "third_party/lwip/src/core.c")
		if got, err := os.ReadFile(file); err != nil || string(got) != "old source\n" {
			t.Fatalf("download followed branch head instead of pin: %s %v", got, err)
		}
		t.Setenv("GIT_CONFIG_KEY_0", "url."+filepath.ToSlash(filepath.Join(t.TempDir(), "missing"))+".insteadOf")
		if _, err := ensureLWIPSource(root); err != nil {
			t.Fatalf("offline reuse: %v", err)
		}
		write(file, "local edit\n")
		if _, err := ensureLWIPSource(root); err == nil || !strings.Contains(err.Error(), "sources differ") {
			t.Fatalf("modified cache accepted: %v", err)
		}
		if got, _ := os.ReadFile(file); string(got) != "local edit\n" {
			t.Fatal("local edit overwritten")
		}
		assertLock(root, original)
		if got, _ := os.ReadFile(filepath.Join(root, "checksum_generated.h")); string(got) != "generated sentinel\n" {
			t.Fatal("source preparation rewrote generated files")
		}
	})
	t.Run("digest-mismatch", func(t *testing.T) {
		bad := lock
		bad.TreeSHA256 = strings.Repeat("0", 64)
		root, original := prepareRoot(t, bad)
		if _, err := ensureLWIPSource(root); err == nil || !strings.Contains(err.Error(), "locked source tree") {
			t.Fatalf("incorrect digest accepted: %v", err)
		}
		if _, err := os.Stat(filepath.Join(root, "third_party/lwip")); !os.IsNotExist(err) {
			t.Fatal("unverified source was installed")
		}
		assertLock(root, original)
	})
	t.Run("check-remote-without-cache", func(t *testing.T) {
		root, original := prepareRoot(t, lock)
		if err := syncSourceCommand(root, []string{"--check"}); err == nil {
			t.Fatal("offline check accepted absent cache")
		}
		if err := syncSourceCommand(root, []string{"--check", "--remote"}); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(root, "third_party/lwip")); !os.IsNotExist(err) {
			t.Fatal("read-only check populated the cache")
		}
		assertLock(root, original)
	})
	t.Run("explicit-pin-update", func(t *testing.T) {
		root, _ := prepareRoot(t, lock)
		if err := syncSourceCommand(root, nil); err != nil {
			t.Fatal(err)
		}
		if err := syncSourceCommand(root, []string{"--revision", second}); err != nil {
			t.Fatal(err)
		}
		got, err := verifyLWIPSource(root)
		if err != nil || got.Revision != second || got.TreeSHA256 == lock.TreeSHA256 {
			t.Fatalf("pin update: %v, %v", got, err)
		}
		if err := generateCommand(root, []string{"--check"}); err != nil {
			t.Fatal(err)
		}
	})
}

func TestSourceSnapshotPinsCommitAndPreservesEdits(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	repository, root := t.TempDir(), t.TempDir()
	write := func(name, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git := func(args ...string) string {
		t.Helper()
		text, err := output(repository, []string{"git"}, args...)
		if err != nil {
			t.Fatal(err)
		}
		return text
	}
	git("init", "-b", "main")
	git("config", "user.name", "Source Test")
	git("config", "user.email", "source@example.invalid")
	write(filepath.Join(repository, "LICENSE"), "test license\n")
	write(filepath.Join(repository, "src/core.c"), "int value = 1;\n")
	write(filepath.Join(repository, "src/obsolete.c"), "/* removed in the next revision */\n")
	write(filepath.Join(repository, "README.md"), "not part of the source snapshot\n")
	git("add", ".")
	git("commit", "-m", "initial source")
	first := git("rev-parse", "HEAD")
	write(filepath.Join(repository, "src/core.c"), "int value = 2;\n")
	git("rm", "src/obsolete.c")
	git("add", ".")
	git("commit", "-m", "update source")
	second := git("rev-parse", "HEAD")

	snapshot, err := fetchSourceSnapshot(repository, first)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot) != 3 || string(snapshot["src/core.c"]) != "int value = 1;\n" {
		t.Fatal("fetch did not select the exact old commit and source paths")
	}
	if err := os.MkdirAll(filepath.Join(root, "third_party"), 0o755); err != nil {
		t.Fatal(err)
	}
	lock := sourceLock{"https://github.com/example/lwip.git", first, sourceTreeHash(snapshot)}
	if err := installSourceSnapshot(root, lock, snapshot, true); err != nil {
		t.Fatal(err)
	}
	if err := syncSourceCommand(root, []string{"--check"}); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "third_party/lwip/src/core.c")
	write(file, "local edits\n")
	if err := syncSourceCommand(root, []string{"--revision", second}); err == nil || !strings.Contains(err.Error(), "sources differ") {
		t.Fatalf("local edits were not rejected before fetching: %v", err)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "local edits\n" {
		t.Fatal("source update overwrote local edits")
	}
	write(file, string(snapshot["src/core.c"]))
	updated, err := fetchSourceSnapshot(repository, second)
	if err != nil {
		t.Fatal(err)
	}
	lock.Revision, lock.TreeSHA256 = second, sourceTreeHash(updated)
	if err := installSourceSnapshot(root, lock, updated, true); err != nil {
		t.Fatal(err)
	}
	if got, err := verifyLWIPSource(root); err != nil || got != lock {
		t.Fatalf("updated snapshot/lock verification: %v, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(root, "third_party/lwip/src/obsolete.c")); !os.IsNotExist(err) {
		t.Fatal("obsolete source survived synchronization")
	}
}
