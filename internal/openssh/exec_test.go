package openssh

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	streamCancelAfter = 200 * time.Millisecond
	streamMustEndIn   = 5 * time.Second
	streamExitCode    = 3
)

func TestPublicKeyOfMatchesTheGeneratedPublicKey(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	login := generateTestKey(t, filepath.Join(dir, "login"))
	other := generateTestKey(t, filepath.Join(dir, "other"))

	derived, err := PublicKeyOf(context.Background(), login)
	if err != nil {
		t.Fatal(err)
	}

	public := readTestKey(t, login+".pub")
	if !SameKey(derived, public) {
		t.Fatalf("derived %q, public %q", derived, public)
	}

	// The comment is not part of the key.
	fields := strings.Fields(public)
	if !SameKey(public, fields[0]+" "+fields[1]+" renamed") {
		t.Fatal("a renamed key should still match")
	}

	if SameKey(derived, readTestKey(t, other+".pub")) {
		t.Fatal("different keys matched")
	}

	if SameKey("", "") || SameKey("ssh-ed25519", "ssh-ed25519") {
		t.Fatal("a line without a key matched")
	}

	_, err = PublicKeyOf(context.Background(), filepath.Join(dir, "missing"))
	if err == nil {
		t.Fatal("a missing key should fail")
	}
}

func TestStreamReportsEachLineAndTheExit(t *testing.T) {
	t.Parallel()

	var lines []string

	err := Stream(t.Context(), func(line string) { lines = append(lines, line) },
		"sh", "-c", "echo one >&2; echo two >&2; exit 3")

	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != streamExitCode || !slices.Equal(lines, []string{"one", "two"}) {
		t.Fatalf("lines = %q, err = %v", lines, err)
	}
}

func TestStreamEndsWithTheContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), streamCancelAfter)
	defer cancel()

	started := time.Now()

	err := Stream(ctx, func(string) {}, "sleep", "30")
	if err == nil || time.Since(started) > streamMustEndIn {
		t.Fatalf("err = %v after %s", err, time.Since(started))
	}
}

func generateTestKey(t *testing.T, private string) string {
	t.Helper()

	_, err := Run(context.Background(), "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", private)
	if err != nil {
		t.Fatal(err)
	}

	return private
}

func readTestKey(t *testing.T, path string) string {
	t.Helper()

	payload, err := os.ReadFile(path) // #nosec G304 -- a key this test generated under t.TempDir.
	if err != nil {
		t.Fatal(err)
	}

	return strings.TrimSpace(string(payload))
}
