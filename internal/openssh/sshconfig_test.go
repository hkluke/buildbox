package openssh

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	userHostStar      = "Host *\n  User other\n"
	testBox           = "dev"
	testAddress       = "100.64.0.2"
	testUser          = "agent"
	testKeyName       = "id_ed25519"
	testBoxKnownHosts = "~/.ssh/buildbox/known_hosts"
)

func tempSSHPaths(t *testing.T) Paths {
	t.Helper()

	paths := pathsIn(t.TempDir())

	err := os.MkdirAll(filepath.Dir(paths.BoxConfig), DirPerm)
	if err != nil {
		t.Fatal(err)
	}

	return paths
}

func readText(t *testing.T, path string) string {
	t.Helper()

	payload, err := os.ReadFile(path) // #nosec G304 -- a file this test wrote under t.TempDir.
	if err != nil {
		t.Fatal(err)
	}

	return string(payload)
}

func TestIncludeCreatesAMissingConfigOnce(t *testing.T) {
	t.Parallel()

	paths := tempSSHPaths(t)

	added, problem := EnsureInclude(paths)
	if !added || problem != "" || readText(t, paths.UserConfig) != includeBlock {
		t.Fatalf("added = %v, problem = %q, config:\n%s", added, problem, readText(t, paths.UserConfig))
	}

	added, problem = EnsureInclude(paths)
	if added || problem != "" || readText(t, paths.UserConfig) != includeBlock {
		t.Fatalf("second call: added = %v, problem = %q", added, problem)
	}
}

func TestIncludeGoesOnTopAndKeepsTheRest(t *testing.T) {
	t.Parallel()

	paths := tempSSHPaths(t)

	err := os.WriteFile(paths.UserConfig, []byte(userHostStar), 0o644) //nolint:gosec // tests the mode is kept.
	if err != nil {
		t.Fatal(err)
	}

	added, problem := EnsureInclude(paths)
	if !added || problem != "" {
		t.Fatalf("added = %v, problem = %q", added, problem)
	}

	if got := readText(t, paths.UserConfig); got != includeBlock+userHostStar {
		t.Fatalf("config:\n%s", got)
	}

	info, err := os.Stat(paths.UserConfig)
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("mode = %v, err = %v", info.Mode().Perm(), err)
	}
}

func TestIncludeLeavesASymlinkedConfigAlone(t *testing.T) {
	t.Parallel()

	paths := tempSSHPaths(t)
	target := filepath.Join(filepath.Dir(paths.UserConfig), "dotfiles-config")

	err := os.WriteFile(target, []byte(userHostStar), FilePerm)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Symlink(target, paths.UserConfig)
	if err != nil {
		t.Fatal(err)
	}

	added, problem := EnsureInclude(paths)
	if added || problem == "" {
		t.Fatalf("added = %v, problem = %q", added, problem)
	}

	if readText(t, target) != userHostStar {
		t.Fatal("the symlink target changed")
	}
}

func TestIncludeRecognizesOtherSpellings(t *testing.T) {
	t.Parallel()

	paths := pathsIn("/home/ada")

	for _, line := range []string{
		IncludeLine,
		"include buildbox/config",
		"Include=/home/ada/.ssh/buildbox/config",
		`Include "~/.ssh/buildbox/config" ~/.ssh/other`,
	} {
		if !hasInclude(userHostStar+line+"\n", paths) {
			t.Fatalf("missed %q", line)
		}
	}

	for _, line := range []string{"# " + IncludeLine, "Include ~/.ssh/other/config"} {
		if hasInclude(line+"\n", paths) {
			t.Fatalf("matched %q", line)
		}
	}
}

// writeHosts puts dev in buildbox's file, and old plus a second dev in the
// user's config, as a box from before the move and a leftover would be.
func writeHosts(t *testing.T, paths Paths) {
	t.Helper()

	block := func(name, address string) string {
		return hostBlock(name, address, testUser, "kh")
	}

	err := os.WriteFile(paths.BoxConfig, []byte(block(testBox, testAddress)), FilePerm)
	if err != nil {
		t.Fatal(err)
	}

	legacy := userHostStar + block("old", "100.64.0.9") + block(testBox, testAddress)

	err = os.WriteFile(paths.UserConfig, []byte(legacy), FilePerm)
	if err != nil {
		t.Fatal(err)
	}
}

func TestFindHostsChecksBuildboxFileThenLegacy(t *testing.T) {
	t.Parallel()

	paths := tempSSHPaths(t)
	writeHosts(t, paths)

	hosts, err := FindHosts(paths, testBox)
	if err != nil || len(hosts) != 2 || hosts[0].Place.Config != paths.BoxConfig || hosts[0].Address != testAddress {
		t.Fatalf("dev = %+v, err = %v", hosts, err)
	}

	hosts, err = FindHosts(paths, "old")
	if err != nil || len(hosts) != 1 || !paths.IsLegacy(hosts[0].Place) {
		t.Fatalf("old = %+v, err = %v", hosts, err)
	}

	if hosts[0].Place.KnownHosts != paths.UserKnownHosts {
		t.Fatalf("old known_hosts = %q", hosts[0].Place.KnownHosts)
	}
}

func TestFindHostsWithoutFiles(t *testing.T) {
	t.Parallel()

	hosts, err := FindHosts(tempSSHPaths(t), testBox)
	if err != nil || len(hosts) != 0 {
		t.Fatalf("hosts = %+v, err = %v", hosts, err)
	}
}

func TestTokenUsesTilde(t *testing.T) {
	t.Parallel()

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	got := Token(filepath.Join(home, ".ssh", testKeyName))
	if got != "~/.ssh/"+testKeyName {
		t.Fatalf("got %q", got)
	}

	spaced := Token(filepath.Join(home, ".ssh", "my key"))
	if !strings.HasPrefix(spaced, `"`) || strings.Contains(spaced, "~/") {
		t.Fatalf("spaced %q", spaced)
	}
}

func TestHostBlockHasNoIdentity(t *testing.T) {
	t.Parallel()

	block := hostBlock("build", testAddress, testUser, testBoxKnownHosts)
	for _, want := range []string{
		"Host build\n",
		"  HostName " + testAddress + "\n",
		"  User agent\n",
		"  UserKnownHostsFile " + testBoxKnownHosts + "\n",
		"  ForwardAgent no\n",
		"  SetEnv TERM=xterm-256color\n",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("missing %q\n%s", want, block)
		}
	}

	// Tailscale SSH decides the login by the device's identity, so there is
	// no key to point ssh at.
	if strings.Contains(block, "Identit") {
		t.Fatalf("identity in block:\n%s", block)
	}
}
