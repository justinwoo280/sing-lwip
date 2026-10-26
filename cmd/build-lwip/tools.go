package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

func envDefault(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

// splitWords handles quoted compiler paths and arguments without invoking a shell.
// Backslashes in double quotes are preserved unless they escape a quote or slash,
// so quoted Windows SDK paths work as well as POSIX command lines.
func splitWords(text string) ([]string, error) {
	var result []string
	var word strings.Builder
	var quote rune
	started := false
	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		if c == '\\' && quote != '\'' {
			if i+1 == len(runes) {
				return nil, fmt.Errorf("trailing escape in %q", text)
			}
			next := runes[i+1]
			if quote == 0 || next == '"' || next == '\\' {
				word.WriteRune(next)
				i++
				started = true
				continue
			}
		}
		if quote != 0 {
			if c == quote {
				quote = 0
			} else {
				word.WriteRune(c)
			}
		} else if c == '\'' || c == '"' {
			quote = c
			started = true
		} else if unicode.IsSpace(c) {
			if started {
				result = append(result, word.String())
				word.Reset()
				started = false
			}
		} else {
			word.WriteRune(c)
			started = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote in %q", text)
	}
	if started {
		result = append(result, word.String())
	}
	return result, nil
}

func commandWords(text string) ([]string, error) {
	words, err := splitWords(text)
	if err == nil && (len(words) == 0 || words[0] == "") {
		err = fmt.Errorf("empty tool command")
	}
	return words, err
}

func process(root string, env map[string]string, tool []string, args ...string) *exec.Cmd {
	cmd := exec.Command(tool[0], append(append([]string(nil), tool[1:]...), args...)...)
	cmd.Dir = root
	cmd.Env = os.Environ()
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	return cmd
}

func run(root string, env map[string]string, tool []string, args ...string) error {
	cmd := process(root, env, tool, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", cmd.String(), err)
	}
	return nil
}

func output(root string, tool []string, args ...string) (string, error) {
	return outputEnv(root, nil, tool, args...)
}

func outputEnv(root string, env map[string]string, tool []string, args ...string) (string, error) {
	cmd := process(root, env, tool, args...)
	data, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %w\n%s", cmd.String(), err, data)
	}
	return strings.TrimSpace(string(data)), nil
}

func compilerIdentity(root string, cc []string) (string, error) {
	// Zig 0.15 forwards --version to Clang without translating its extended
	// target syntax (for example gnu.2.17). Target options do not affect the
	// compiler identity, so omit them from this metadata-only invocation.
	command := []string{cc[0]}
	for i := 1; i < len(cc); i++ {
		if cc[i] == "-target" || cc[i] == "--target" {
			i++
			continue
		}
		if strings.HasPrefix(cc[i], "--target=") || strings.HasPrefix(cc[i], "-target=") {
			continue
		}
		command = append(command, cc[i])
	}
	return output(root, command, "--version")
}

func includes(root string) []string {
	return []string{"-I" + root, "-I" + filepath.Join(root, "third_party/lwip/src/include"), "-I" + filepath.Join(root, "third_party/lwip/src/ports/include")}
}

func wrappers(root string) ([]string, error) {
	paths, err := filepath.Glob(filepath.Join(root, "lwip_*.c"))
	if err == nil && len(paths) == 0 {
		err = fmt.Errorf("no cgo compilation units; run generate first")
	}
	return paths, err
}

func nativeDependencies(root string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(filepath.Join(root, "third_party/lwip/src"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && (filepath.Ext(path) == ".c" || filepath.Ext(path) == ".h") {
			paths = append(paths, path)
		}
		return nil
	})
	sort.Slice(paths, func(i, j int) bool { return filepath.ToSlash(paths[i]) < filepath.ToSlash(paths[j]) })
	return paths, err
}

func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func copyFile(source, target string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return os.WriteFile(target, data, 0o644)
}
