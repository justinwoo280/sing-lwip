package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
	if err := installSourceSnapshot(root, lock, snapshot); err != nil {
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
	if err := installSourceSnapshot(root, lock, updated); err != nil {
		t.Fatal(err)
	}
	if got, err := verifyLWIPSource(root); err != nil || got != lock {
		t.Fatalf("updated snapshot/lock verification: %v, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(root, "third_party/lwip/src/obsolete.c")); !os.IsNotExist(err) {
		t.Fatal("obsolete source survived synchronization")
	}
}
