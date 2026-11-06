package hetzner

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hkluke/buildbox/internal/abort"
)

const tokenDocs = "generating-api-token"

// A missing token says where to get one. These tests set the environment, so
// they cannot run in parallel.
func TestAMissingTokenSaysWhereToGetOne(t *testing.T) {
	t.Setenv("HCLOUD_TOKEN", "")
	t.Setenv("HCLOUD_CONTEXT", "")
	t.Setenv("HCLOUD_CONFIG", filepath.Join(t.TempDir(), "missing.toml"))

	var failed *abort.Error

	_, err := NewClient("test")
	if !errors.As(err, &failed) || !strings.Contains(failed.Hint, tokenDocs) {
		t.Fatalf("err = %#v", err)
	}
}

func TestAContextWithoutATokenSaysWhereToGetOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cli.toml")

	err := os.WriteFile(path, []byte("active_context = \"buildbox\"\n\n[[contexts]]\n  name = \"buildbox\"\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("HCLOUD_TOKEN", "")
	t.Setenv("HCLOUD_CONTEXT", "")
	t.Setenv("HCLOUD_CONFIG", path)

	var failed *abort.Error

	_, err = NewClient("test")
	if !errors.As(err, &failed) || failed.Headline != "hcloud context buildbox has no token" ||
		!strings.Contains(failed.Hint, tokenDocs) {
		t.Fatalf("err = %#v", err)
	}
}
