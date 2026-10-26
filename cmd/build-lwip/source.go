package main

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const sourceLockFile = "third_party/lwip.lock.json"

type sourceLock struct {
	Repository string `json:"repository"`
	Revision   string `json:"revision"`
	TreeSHA256 string `json:"tree_sha256"`
}

func readSourceLock(root string) (sourceLock, error) {
	var lock sourceLock
	data, err := os.ReadFile(filepath.Join(root, sourceLockFile))
	if err != nil {
		return lock, err
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		return lock, err
	}
	repository, err := url.Parse(lock.Repository)
	if err != nil || repository.Scheme != "https" || repository.Host != "github.com" || repository.User != nil || repository.RawQuery != "" || repository.Fragment != "" || len(strings.Split(strings.Trim(repository.Path, "/"), "/")) != 2 {
		return lock, fmt.Errorf("%s: expected a public GitHub repository URL", sourceLockFile)
	}
	if !validDigest(lock.Revision, 40) || !validDigest(lock.TreeSHA256, 64) {
		return lock, fmt.Errorf("%s: expected a full Git commit and SHA256 tree digest", sourceLockFile)
	}
	return lock, nil
}

func validDigest(value string, size int) bool {
	_, err := hex.DecodeString(value)
	return len(value) == size && value == strings.ToLower(value) && err == nil
}

func sourceFile(name string) bool {
	return name == "LICENSE" || strings.HasPrefix(name, "src/")
}

func readSourceSnapshot(directory string) (map[string][]byte, error) {
	files := make(map[string][]byte)
	err := filepath.WalkDir(directory, func(name string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(directory, name)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if !sourceFile(relative) || !entry.Type().IsRegular() {
			return fmt.Errorf("unexpected file in lwIP source snapshot: %s", relative)
		}
		files[relative], err = os.ReadFile(name)
		return err
	})
	return files, err
}

func sourceTreeHash(files map[string][]byte) string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	digest := sha256.New()
	for _, name := range names {
		fmt.Fprintf(digest, "%x  %s\n", sha256.Sum256(files[name]), name)
	}
	return fmt.Sprintf("%x", digest.Sum(nil))
}

func verifyLWIPSource(root string) (sourceLock, error) {
	lock, err := readSourceLock(root)
	if err != nil {
		return lock, err
	}
	files, err := readSourceSnapshot(filepath.Join(root, "third_party/lwip"))
	if err != nil {
		return lock, err
	}
	if sourceTreeHash(files) != lock.TreeSHA256 {
		return lock, fmt.Errorf("lwIP sources differ from %s; commit native changes to the lwIP fork, then sync its revision", sourceLockFile)
	}
	return lock, nil
}

func fetchSourceSnapshot(repository, revision string) (map[string][]byte, error) {
	if !validDigest(revision, 40) {
		return nil, fmt.Errorf("lwIP revision must be a full 40-character commit SHA")
	}
	directory, err := os.MkdirTemp("", "sing-lwip-source-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(directory)
	for _, args := range [][]string{{"init", "--quiet"}, {"fetch", "--quiet", "--depth=1", "--no-tags", repository, revision}} {
		if err := run(directory, nil, []string{"git"}, args...); err != nil {
			return nil, err
		}
	}
	head, err := output(directory, []string{"git"}, "rev-parse", "FETCH_HEAD^{commit}")
	if err != nil {
		return nil, err
	}
	if head != revision {
		return nil, fmt.Errorf("lwIP fetch resolved %s, expected %s", head, revision)
	}
	data, err := process(directory, nil, []string{"git"}, "archive", "--format=tar", head, "LICENSE", "src").Output()
	if err != nil {
		return nil, fmt.Errorf("archive lwIP commit: %w", err)
	}
	files := make(map[string][]byte)
	archive := tar.NewReader(bytes.NewReader(data))
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if header.Typeflag == tar.TypeDir || header.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		name := header.Name
		if header.Typeflag != tar.TypeReg || path.Clean(name) != name || !filepath.IsLocal(name) || strings.ContainsAny(name, "\\\r\n") || !sourceFile(name) {
			return nil, fmt.Errorf("unsupported lwIP archive entry %q", name)
		}
		files[name], err = io.ReadAll(archive)
		if err != nil {
			return nil, err
		}
	}
	if files["LICENSE"] == nil || len(files) < 2 {
		return nil, fmt.Errorf("lwIP commit must contain LICENSE and src/")
	}
	return files, nil
}

func syncSourceCommand(root string, args []string) error {
	flags := flag.NewFlagSet("sync-lwip", flag.ContinueOnError)
	revision := flags.String("revision", "", "full commit SHA to pin (default: current lock)")
	check := flags.Bool("check", false, "verify the checked-in source snapshot without changing files")
	remote := flags.Bool("remote", false, "also fetch and verify the pinned repository commit")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if *check && *revision != "" {
		return fmt.Errorf("--check cannot update --revision")
	}
	lock, err := readSourceLock(root)
	if err != nil {
		return err
	}
	target := filepath.Join(root, "third_party/lwip")
	// Preserve local edits. Updates begin from a verified snapshot or an absent one.
	if _, err := os.Stat(target); err == nil || *check {
		if _, err := verifyLWIPSource(root); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if *check && !*remote {
		fmt.Println("Verified lwIP", lock.Revision, lock.TreeSHA256)
		return nil
	}
	if *revision != "" {
		lock.Revision = *revision
	}
	files, err := fetchSourceSnapshot(lock.Repository, lock.Revision)
	if err != nil {
		return err
	}
	digest := sourceTreeHash(files)
	if (*check || *revision == "") && digest != lock.TreeSHA256 {
		return fmt.Errorf("lwIP repository commit does not match the locked source tree")
	}
	if !*check {
		lock.TreeSHA256 = digest
		if err := installSourceSnapshot(root, lock, files); err != nil {
			return err
		}
		if err := generateCommand(root, nil); err != nil {
			return err
		}
	}
	fmt.Println("Verified lwIP", lock.Repository, lock.Revision, digest)
	return nil
}

func installSourceSnapshot(root string, lock sourceLock, files map[string][]byte) error {
	parent := filepath.Join(root, "third_party")
	directory, err := os.MkdirTemp(parent, ".lwip-sync-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	payload, target := filepath.Join(directory, "source"), filepath.Join(parent, "lwip")
	for name, data := range files {
		file := filepath.Join(payload, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(file, data, 0o644); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return err
	}
	lockFile := filepath.Join(directory, "lock.json")
	if err := os.WriteFile(lockFile, append(data, '\n'), 0o644); err != nil {
		return err
	}
	backup := filepath.Join(directory, "previous")
	if err := os.Rename(target, backup); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(payload, target); err != nil {
		_ = os.Rename(backup, target)
		return err
	}
	err = os.Rename(lockFile, filepath.Join(root, sourceLockFile))
	if err != nil {
		_ = os.RemoveAll(target)
		_ = os.Rename(backup, target)
	}
	return err
}
