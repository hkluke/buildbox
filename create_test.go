package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hkluke/buildbox/internal/abort"
	"github.com/hkluke/buildbox/internal/config"
	"github.com/hkluke/buildbox/internal/console"
	"github.com/hkluke/buildbox/internal/openssh"
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

// The box's own key is made on the box and reported by create, which reads
// it before any coding agent has run there. A second run must not replace
// it: whoever registered the first key would be left with a dead one.
func TestBoxKeyIsMadeOnTheBoxAndNeverReplaced(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	made := time.Date(2026, time.November, 17, 23, 30, 0, 0, time.UTC)

	out, err := asBox(t, home, boxKeyCommand(testBox, made))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	// The comment says which box and from when, since box names are reused.
	public := strings.TrimSpace(out)
	if !strings.HasPrefix(public, publicKeyPrefix) || !strings.HasSuffix(public, " agent@dev 2026-11-17") {
		t.Fatalf("public key = %q", public)
	}

	assertPerm(t, filepath.Join(home, ".ssh"), openssh.DirPerm)
	assertPerm(t, filepath.Join(home, ".ssh", boxKeyName), openssh.FilePerm)

	again, err := asBox(t, home, boxKeyCommand(testBox, made.AddDate(0, 0, 1)))
	if err == nil {
		t.Fatalf("a second run made another key:\n%s", again)
	}

	if kept := readFile(t, filepath.Join(home, ".ssh", boxKeyName+".pub")); strings.TrimSpace(kept) != public {
		t.Fatalf("the key changed: %q, was %q", kept, public)
	}
}

// keychain warns at every login about a key without its public half. The
// half is derived on the box from the key itself, so it cannot be the wrong
// one. A key with a passphrase gives none, and create must get past that
// without a word, and without ssh-keygen asking anyone.
func TestGitKeyGetsItsPublicHalfUnlessItHasAPassphrase(t *testing.T) {
	t.Parallel()

	for _, check := range []struct {
		name       string
		passphrase string
		derived    bool
	}{
		{"without a passphrase", "", true},
		{"with a passphrase", "correct horse", false},
	} {
		t.Run(check.name, func(t *testing.T) {
			t.Parallel()

			source := filepath.Join(t.TempDir(), "key")

			_, err := openssh.Run(t.Context(), "ssh-keygen", "-q", "-t", "ed25519", "-N", check.passphrase, "-f", source)
			if err != nil {
				t.Skip("no ssh-keygen to make a key with")
			}

			home := t.TempDir()
			copied := filepath.Join(home, ".ssh", config.GitKeyName)
			writeFile(t, copied, readFile(t, source))

			out, err := asBox(t, home, gitKeyCommand())
			if err != nil || out != "" {
				t.Fatalf("err = %v, output = %q", err, out)
			}

			assertPerm(t, copied, openssh.FilePerm)

			derived, err := os.ReadFile(copied + ".pub") // #nosec G304 -- under t.TempDir.
			if !check.derived {
				if !os.IsNotExist(err) {
					t.Fatalf("a public half without the passphrase: %q, %v", derived, err)
				}

				return
			}

			want := strings.Fields(readFile(t, source+".pub"))[:2]
			if err != nil || !slices.Equal(strings.Fields(string(derived))[:2], want) {
				t.Fatalf("derived %q (%v), want %v", derived, err, want)
			}
		})
	}
}

// A server that is shown a key can tell whose it is, so the Git key goes to
// the two hosts it was made for and to no other. Every other host gets the
// box's own key, which ssh finds by its name, and IdentitiesOnly keeps the
// ssh-agent, which holds the Git key, from offering it there as well.
func TestBoxSSHConfigShowsTheGitKeyToItsHostsAlone(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	path := filepath.Join(home, ".ssh", "config")
	writeFile(t, path, "an earlier config\n")

	out, err := asBox(t, home, sshConfigCommand())
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	assertPerm(t, path, openssh.FilePerm)

	if written := readFile(t, path); written != boxSSHConfig {
		t.Fatalf("config on the box:\n%s", written)
	}

	gitKey := "identityfile ~/.ssh/" + config.GitKeyName + "\n"
	boxKey := "identityfile ~/.ssh/" + boxKeyName + "\n"

	for _, check := range []struct{ host, offered, withheld string }{
		{"github.com", gitKey, boxKey},
		{"gitlab.com", gitKey, boxKey},
		{"bitbucket.org", boxKey, gitKey},
		{"example.com", boxKey, gitKey},
		{"www.github.com", boxKey, gitKey},
	} {
		resolved := resolveSSH(t, path, check.host)
		if !strings.Contains(resolved, check.offered) || strings.Contains(resolved, check.withheld) ||
			!strings.Contains(resolved, "identitiesonly yes\n") {
			t.Fatalf("%s:\n%s", check.host, resolved)
		}
	}
}

func TestCreateEndsWithTheBoxKey(t *testing.T) {
	t.Parallel()

	const public = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAISAMPLE agent@dev 2026-11-17"

	var out bytes.Buffer

	printCreateDone(createPlan{name: testBox, con: console.NewWriter(&out)}, time.Minute, false, public)

	for _, want := range []string{
		testBox + " is ready",
		"ssh " + testBox,
		"the box's own SSH key",
		"register its public half",
		public + "\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in:\n%s", want, out.String())
		}
	}
}

// asBox runs a command the way create does on a box: in bash, as a user
// whose home is the given directory, and with nothing to read.
func asBox(t *testing.T, home, command string) (string, error) {
	t.Helper()

	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash to stand in for a box")
	}

	_, err = os.Stat(sshKeygen)
	if err != nil {
		t.Skip("no " + sshKeygen + " to stand in for a box")
	}

	// #nosec G204 -- bash from the PATH runs a command this program wrote.
	cmd := exec.CommandContext(t.Context(), bash, "-c", command)

	cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}

	out, err := cmd.Output()

	return string(out), err
}

// resolveSSH is what ssh makes of a config for one host, without connecting.
func resolveSSH(t *testing.T, configPath, host string) string {
	t.Helper()

	result, err := openssh.Run(t.Context(), "ssh", "-G", "-F", configPath, host)
	if err != nil {
		t.Skip("no ssh to read the config with")
	}

	return strings.ToLower(result.Stdout)
}

func assertPerm(t *testing.T, path string, want os.FileMode) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != want {
		t.Fatalf("%s: %v, %v, want mode %v", path, info, err, want)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	payload, err := os.ReadFile(path) // #nosec G304 -- a file a test put under t.TempDir.
	if err != nil {
		t.Fatal(err)
	}

	return string(payload)
}

// writeFile leaves the file readable by everyone, as scp may: it is the
// command under test that has to close it.
func writeFile(t *testing.T, path, content string) {
	t.Helper()

	err := os.MkdirAll(filepath.Dir(path), openssh.DirPerm)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(path, []byte(content), 0o644) // #nosec G306 -- on purpose, see above.
	if err != nil {
		t.Fatal(err)
	}
}
