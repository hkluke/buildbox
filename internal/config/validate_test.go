package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFakekeyforbuildboxtest comment\n"

func TestInvalidPrivateKeyWantsAPrivateKeyFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	public := filepath.Join(dir, "buildbox_git_ed25519.pub")
	private := filepath.Join(dir, "buildbox_git_ed25519")

	writeTestFile(t, public, testPublicKey)
	writeTestFile(t, private, "private\n")

	problem := InvalidPrivateKey(public)
	if !strings.Contains(problem, "public key") {
		t.Fatalf("public key problem %q", problem)
	}

	problem = InvalidPrivateKey(filepath.Join(dir, "missing"))
	if !strings.Contains(problem, "Could not read") {
		t.Fatalf("missing problem %q", problem)
	}

	problem = InvalidPrivateKey("  ")
	if !strings.Contains(problem, "required") {
		t.Fatalf("empty problem %q", problem)
	}

	problem = InvalidPrivateKey(private)
	if problem != "" {
		t.Fatal(problem)
	}
}

func TestInvalidTag(t *testing.T) {
	t.Parallel()

	for _, good := range []string{DefaultTag, "tag:buildbox-luca", "tag:ci2"} {
		if problem := InvalidTag(good); problem != "" {
			t.Fatalf("%s: %s", good, problem)
		}
	}

	for _, bad := range []string{"", "buildbox", "tag:", "tag:Build", "tag:build box", "tag:-x"} {
		if InvalidTag(bad) == "" {
			t.Fatalf("%q accepted", bad)
		}
	}
}

// An unknown release passes: that is Hetzner's to refuse at create.
func TestInvalidImageWantsAnUbuntuImage(t *testing.T) {
	t.Parallel()

	for _, good := range []string{DefaultImage, "ubuntu-24.04", " ubuntu-28.04 ", "ubuntu-26.10"} {
		if problem := InvalidImage(good); problem != "" {
			t.Fatalf("%q: %s", good, problem)
		}
	}

	for _, bad := range []string{"", "debian-13", "Ubuntu-26.04", "ubuntu", "ubuntu 26.04", "fedora-44"} {
		problem := InvalidImage(bad)
		if problem == "" || !strings.Contains(problem, DefaultImage) {
			t.Fatalf("%q: %q", bad, problem)
		}
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()

	err := os.WriteFile(path, []byte(content), ConfigPerm)
	if err != nil {
		t.Fatal(err)
	}
}
