package cloudinit

import (
	"strings"
	"testing"

	"github.com/hkluke/buildbox/internal/config"
)

func TestRenderUserDataInstallsChosenTools(t *testing.T) {
	t.Parallel()

	doc, err := Render(config.Config{
		AITools: config.ToolGrok + " " + config.ToolClaude,
		Tag:     config.DefaultTag,
	}, "box", "tskey")
	if err != nil {
		t.Fatal(err)
	}

	assertToolLines(t, doc, config.ToolGrok, config.ToolClaude)

	if !strings.Contains(doc, "su', '-', '"+config.AgentUser+"'") {
		t.Fatalf("agent install:\n%s", doc)
	}

	plain, err := Render(config.Config{
		AITools: config.ToolNone,
	}, "box", "tskey")
	if err != nil {
		t.Fatal(err)
	}

	for _, tool := range config.Tools() {
		if strings.Contains(plain, tool.Command) {
			t.Fatalf("none installed %s\n%s", tool.ID, plain)
		}
	}

	legacy, err := Render(config.Config{}, "box", "tskey")
	if err != nil {
		t.Fatal(err)
	}

	assertToolLines(t, legacy, config.ToolOpenCode)
}

// The reboot and the kept network config are explained in the document;
// the four packages are what the agent cannot install without sudo.
func TestUserDataBootsUpdatedWithAToolchain(t *testing.T) {
	t.Parallel()

	doc, err := Render(config.Config{Tag: config.DefaultTag}, "build-x", "tskey-auth-SAMPLE")
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"package_upgrade: true\n",
		"package_reboot_if_required: true\n",
		"updates:\n  network:\n    when: [boot-new-instance]\n",
		"  - build-essential\n",
		"  - pkg-config\n",
		"  - python3-venv\n",
		"  - unzip\n",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("missing %q\n%s", want, doc)
		}
	}
}

func TestCodexInstallsBubblewrap(t *testing.T) {
	t.Parallel()

	withCodex, err := Render(config.Config{
		AITools: config.ToolCodex,
	}, "box", "tskey")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(withCodex, "  - bubblewrap\n") {
		t.Fatalf("missing bubblewrap\n%s", withCodex)
	}

	without, err := Render(config.Config{
		AITools: config.ToolGrok,
	}, "box", "tskey")
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(without, "bubblewrap") {
		t.Fatal("bubblewrap without codex")
	}
}

// The profile lets whatever sits at agy's path create user namespaces, so it
// belongs on a box only with Antigravity, and loaded before agy first runs.
func TestAntigravityInstallsItsAppArmorProfile(t *testing.T) {
	t.Parallel()

	with, err := Render(config.Config{
		AITools: config.ToolAntigravity,
	}, "box", "tskey")
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"  - path: " + antigravityProfileStaged + "\n",
		"      profile agy /home/" + config.AgentUser + "/.local/bin/agy flags=(unconfined) {\n",
		"        userns,\n",
	} {
		if !strings.Contains(with, want) {
			t.Fatalf("missing %q\n%s", want, with)
		}
	}

	loadAt := strings.Index(with, "apparmor_parser --replace "+antigravityProfileStaged)
	installAt := strings.Index(with, "antigravity.google/cli/install.sh")

	if loadAt < 0 || installAt < loadAt {
		t.Fatalf("the profile is not loaded before agy's installer\n%s", with)
	}

	without, err := Render(config.Config{
		AITools: config.ToolCodex + " " + config.ToolClaude,
	}, "box", "tskey")
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(without, "apparmor") || strings.Contains(without, "userns") {
		t.Fatal("an AppArmor profile without antigravity")
	}
}

func assertToolLines(t *testing.T, doc string, want ...string) {
	t.Helper()

	chosen := map[string]bool{}
	for _, id := range want {
		chosen[id] = true
	}

	for _, tool := range config.Tools() {
		has := strings.Contains(doc, tool.Command)
		if chosen[tool.ID] && !has {
			t.Fatalf("missing %s\n%s", tool.ID, doc)
		}

		if !chosen[tool.ID] && has {
			t.Fatalf("unexpected %s\n%s", tool.ID, doc)
		}
	}
}

func TestUserDataJoinsTaggedWithoutLoginKeys(t *testing.T) {
	t.Parallel()

	doc, err := Render(config.Config{Tag: "tag:dev"}, "box", "tskey")
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"--advertise-tags=tag:dev",
		"'tailscale', 'set', '--auto-update'",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("missing %q\n%s", want, doc)
		}
	}

	if strings.Contains(doc, "ssh_authorized_keys") || strings.Contains(doc, "__") {
		t.Fatalf("login keys or an unfilled placeholder:\n%s", doc)
	}
}

// Disabling the ssh units lasted until the next reboot, since cloud-init's
// own unit wants sshd.service, so the document must not go back to it. The
// two files keep sshd from starting, the package is removed and never
// purged, which would delete the host keys Tailscale SSH presents, and all
// of it is done before the box joins the tailnet. The socket systemd made
// before the mask existed is stopped by pattern, since stopping it by name
// fails on a release that has no such socket.
func TestUserDataLeavesNoOpenSSHServer(t *testing.T) {
	t.Parallel()

	doc, err := Render(config.Config{Tag: config.DefaultTag}, "box", "tskey")
	if err != nil {
		t.Fatal(err)
	}

	const remove = "'remove', '--yes', 'openssh-server', 'openssh-sftp-server']"

	for _, want := range []string{
		"  - path: /etc/ssh/sshd_not_to_be_run\n",
		"  - path: /etc/systemd/system-generators/systemd-ssh-generator\n",
		remove,
		"  - ['systemctl', 'stop', 'sshd-*.socket']\n",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("missing %q\n%s", want, doc)
		}
	}

	for _, unwanted := range []string{"'purge'", "'systemctl', 'disable', '--now', 'ssh"} {
		if strings.Contains(doc, unwanted) {
			t.Fatalf("unexpected %q\n%s", unwanted, doc)
		}
	}

	if strings.Index(doc, remove) > strings.Index(doc, "'tailscale', 'up'") {
		t.Fatalf("the OpenSSH server is removed after the tailnet join:\n%s", doc)
	}
}
