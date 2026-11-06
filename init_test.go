package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hkluke/buildbox/internal/abort"
	"github.com/hkluke/buildbox/internal/config"
	"github.com/hkluke/buildbox/internal/hetzner"
)

func TestInitSeedReadsAnOlderConfig(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.toml")

	err := os.WriteFile(path, []byte("[ssh]\npubkey = \"x\"\n\n[git]\nname = \"Ada Lovelace\"\n"), config.ConfigPerm)
	if err != nil {
		t.Fatal(err)
	}

	seed, err := initSeed(context.Background(), true, path)
	if err != nil {
		t.Fatal(err)
	}

	if seed.GitUserName != "Ada Lovelace" || seed.Tag != config.DefaultTag {
		t.Fatalf("seed = %+v", seed)
	}
}

func TestInitSeedReusesAClientSavedBeforeAnyConfig(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.toml")

	client := config.Config{OAuthClientID: "k1", OAuthClientSecret: "s1"}

	_, err := config.WriteCredentials(config.CredentialsPath(path), client)
	if err != nil {
		t.Fatal(err)
	}

	seed, err := initSeed(context.Background(), false, path)
	if err != nil || seed.OAuthClientID != "k1" || seed.OAuthClientSecret != "s1" {
		t.Fatalf("seed = %+v, %v", seed, err)
	}
}

func TestFirewallFailureKeepsTheAnswersAndNamesANewKey(t *testing.T) {
	t.Parallel()

	cfg := config.Config{GitSSHKeyPath: "/home/ada/.ssh/buildbox_git_ed25519"}

	var failed *abort.Error

	err := firewallFailure(abort.New("Could not create Hetzner firewall"), cfg, true)
	if !errors.As(err, &failed) || !strings.Contains(failed.Hint, "The config is saved") ||
		!strings.Contains(failed.Hint, "buildbox_git_ed25519.pub") {
		t.Fatalf("err = %#v", err)
	}

	if !errors.As(firewallFailure(abort.New("x"), cfg, false), &failed) || strings.Contains(failed.Hint, ".pub") {
		t.Fatalf("a run without a new key named one: %#v", failed)
	}
}

const (
	tokenLink     = "generating-api-token"
	tailscaleLink = "tailscale.com/download"
	exampleLink   = "toml.example"
)

func TestInitNeedsNamesEachMissingThingAndItsFix(t *testing.T) {
	t.Parallel()

	tailscaleDown := abort.Hint("Tailscale is not up on this machine", "Tailscale state is Stopped.", "")
	noToken := abort.Hint("No Hetzner token found", "", hetzner.TokenHint)

	for _, test := range []struct {
		terminal      bool
		tailscale     error
		hetzner       error
		headline      string
		links, absent []string
	}{
		{
			true, nil, noToken, "init needs a Hetzner token first",
			[]string{tokenLink, "setup-hcloud-cli"},
			[]string{tailscaleLink, exampleLink},
		},
		{
			true, tailscaleDown, noToken, "init needs Tailscale and a Hetzner token first",
			[]string{tailscaleLink, tokenLink},
			[]string{exampleLink},
		},
		{
			false, tailscaleDown, noToken, "init needs a terminal, Tailscale, and a Hetzner token first",
			[]string{exampleLink, tailscaleLink, tokenLink},
			nil,
		},
	} {
		var failed *abort.Error
		if !errors.As(initNeedsFailure(test.terminal, test.tailscale, test.hetzner), &failed) {
			t.Fatal("not an abort.Error")
		}

		if failed.Headline != test.headline {
			t.Errorf("headline %q, want %q", failed.Headline, test.headline)
		}

		for _, link := range test.links {
			if !strings.Contains(failed.Hint, link) {
				t.Errorf("%s: hint lacks %s:\n%s", test.headline, link, failed.Hint)
			}
		}

		for _, link := range test.absent {
			if strings.Contains(failed.Hint, link) {
				t.Errorf("%s: hint has %s for something that is fine", test.headline, link)
			}
		}

		if test.tailscale != nil && !strings.Contains(failed.Detail, "Tailscale state is Stopped.") {
			t.Errorf("detail lacks Tailscale's reason: %q", failed.Detail)
		}
	}
}
