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

// The four packages are what the agent cannot install without sudo.
func TestUserDataInstallsAToolchain(t *testing.T) {
	t.Parallel()

	doc, err := Render(config.Config{Tag: config.DefaultTag}, "build-x", "tskey-auth-SAMPLE")
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"package_upgrade: true\n",
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
		"'systemctl', 'disable', '--now', 'ssh.socket', 'ssh.service'",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("missing %q\n%s", want, doc)
		}
	}

	if strings.Contains(doc, "ssh_authorized_keys") || strings.Contains(doc, "__") {
		t.Fatalf("login keys or an unfilled placeholder:\n%s", doc)
	}
}
