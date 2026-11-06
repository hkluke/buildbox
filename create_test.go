package main

import (
	"bytes"
	"context"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/hkluke/buildbox/internal/abort"
	"github.com/hkluke/buildbox/internal/config"
	"github.com/hkluke/buildbox/internal/console"
	"github.com/hkluke/buildbox/internal/tailscale"
)

// Shared by the tests in this package.
const (
	testBox     = "dev"
	testAddress = "100.64.0.2"
)

func TestCreateTakesTheNameEitherWay(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{testBox}, {"--name", testBox}, {"--name=dev"}} {
		name, err := parseCreateArgs(args)
		if err != nil || name != testBox {
			t.Fatalf("%v: name = %q, err = %v", args, name, err)
		}
	}

	name, err := parseCreateArgs(nil)
	if err != nil || !strings.HasPrefix(name, "build-") {
		t.Fatalf("default name = %q, err = %v", name, err)
	}

	for _, args := range [][]string{{testBox, "--name", "other"}, {testBox, "other"}, {"--bogus"}} {
		_, err = parseCreateArgs(args)
		if err == nil {
			t.Fatalf("%v: expected a usage error", args)
		}
	}
}

func TestResolveAuthKeyPrefersTheEnvironment(t *testing.T) {
	t.Setenv("TS_AUTHKEY", "tskey-auth-env")

	// An OAuth client is configured but must not be called: there is no
	// server for it, so a call would fail.
	cfg := config.Config{Tag: config.DefaultTag, OAuthClientID: "client", OAuthClientSecret: "secret"}

	key, err := resolveAuthKey(context.Background(), console.NewWriter(io.Discard), cfg)
	if err != nil {
		t.Fatal(err)
	}

	if key != "tskey-auth-env" {
		t.Fatalf("auth key = %q", key)
	}
}

func TestResolveAuthKeyShowsWhereToGetOne(t *testing.T) {
	t.Setenv("TS_AUTHKEY", "")

	var out bytes.Buffer

	// Without a terminal the prompt returns nothing, which is the error path.
	_, err := resolveAuthKey(context.Background(), console.NewWriter(&out), config.Config{Tag: "tag:dev"})
	if err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("err = %v", err)
	}

	for _, want := range []string{tailscale.KeysURL, "Ephemeral on", "Tags: tag:dev"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in:\n%s", want, out.String())
		}
	}
}

func TestBuildboxSSHUsesItsOwnKnownHosts(t *testing.T) {
	t.Parallel()

	if !slices.Contains(sshBaseArgs("/tmp/kh"), "UserKnownHostsFile=/tmp/kh") {
		t.Fatalf("args = %v", sshBaseArgs("/tmp/kh"))
	}

	if !slices.Contains(sshBaseArgs("/tmp/kh"), "ForwardAgent=no") {
		t.Fatalf("args = %v, want ForwardAgent=no", sshBaseArgs("/tmp/kh"))
	}

	probe := sshProbeArgs(config.AdminUser, testAddress, "/tmp/kh")
	if probe[len(probe)-2] != config.AdminUser+"@"+testAddress || probe[len(probe)-1] != "true" {
		t.Fatalf("probe = %v", probe)
	}
}

func TestPolicyRefusalIsRecognized(t *testing.T) {
	t.Parallel()

	banner := "tailnet policy does not permit you to SSH to this node\r\nadmin@100.64.0.2: Permission denied"
	if !policyRefused(banner) {
		t.Fatal("banner not recognized")
	}

	if policyRefused("ssh: connect to host 100.64.0.2 port 22: Connection refused") {
		t.Fatal("a closed port is not a policy refusal")
	}
}

func TestRequireTagRefusesAnUntaggedBox(t *testing.T) {
	t.Parallel()

	err := requireTag(testBox, config.DefaultTag, nil)
	if err == nil || !strings.Contains(err.Error(), "without "+config.DefaultTag) {
		t.Fatalf("untagged: %v", err)
	}

	err = requireTag(testBox, config.DefaultTag, []string{"tag:other", config.DefaultTag})
	if err != nil {
		t.Fatalf("tagged: %v", err)
	}
}

func TestCreateFailureExplainsAnInterrupt(t *testing.T) {
	t.Parallel()

	stopped, cancel := context.WithCancel(context.Background())
	cancel()

	failed := abort.New("Rootless Docker is not working for agent on dev")

	err := createFailure(stopped, failed, true, testBox)
	if err == nil || !strings.HasPrefix(err.Error(), "Interrupted") || strings.Contains(err.Error(), "Docker") {
		t.Fatalf("interrupted after create: %v", err)
	}

	if !strings.Contains(err.Error(), "delete "+testBox) {
		t.Fatalf("interrupted after create should say how to delete the box: %v", err)
	}

	err = createFailure(stopped, failed, false, testBox)
	if err == nil || err.Error() != "Interrupted" {
		t.Fatalf("interrupted before create: %v", err)
	}
}

func TestCreateFailureKeepsTheStepError(t *testing.T) {
	t.Parallel()

	failed := abort.New("Rootless Docker is not working for agent on dev")

	err := createFailure(context.Background(), failed, true, testBox)
	if err == nil || !strings.HasPrefix(err.Error(), failed.Error()) || !strings.Contains(err.Error(), "delete "+testBox) {
		t.Fatalf("failed after create: %v", err)
	}

	err = createFailure(context.Background(), nil, true, testBox)
	if err != nil {
		t.Fatalf("success: %v", err)
	}
}
