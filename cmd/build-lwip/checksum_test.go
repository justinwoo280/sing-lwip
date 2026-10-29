package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Generated instructions must be reproducible and included in the native hash
// checked by the publisher; a stale or substituted kernel must not go unnoticed.
func TestChecksumGenerationAndProvenance(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "third_party/lwip/src"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"lwip_core.c", "bridge.c", "bridge.h", "lwipopts.h", "checksum.h", "third_party/lwip/src/test.h"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("/* fixture */\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := generateChecksums(root, false); err != nil {
		t.Fatal(err)
	}
	if err := generateChecksums(root, true); err != nil {
		t.Fatal(err)
	}
	before, err := nativeFingerprint(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "checksum_generated.h"), []byte("/* substituted */\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := generateChecksums(root, true); err == nil {
		t.Fatal("stale checksum kernel accepted")
	}
	after, err := nativeFingerprint(root)
	if err != nil || before == after {
		t.Fatalf("native fingerprint missed checksum change: %v", err)
	}
	if err := generateChecksums(root, false); err != nil {
		t.Fatal(err)
	}
	rebuilt, err := nativeFingerprint(root)
	if err != nil || rebuilt != before {
		t.Fatalf("checksum generation is not reproducible: %v", err)
	}
}
