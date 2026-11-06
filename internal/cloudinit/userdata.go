// Package cloudinit renders the user-data document a new server boots with.
package cloudinit

import (
	"slices"
	"strings"

	"github.com/hkluke/buildbox/internal/config"
)

// userDataTemplate is the cloud-init document. Placeholders are replaced
// before the server is created. $KEY_FILE, $RELEASE, and $(...) are left
// for cloud-init and the server shell.
const userDataTemplate = `#cloud-config
package_update: true
package_upgrade: true
packages:
  - git
  - keychain
  - btop
  - tailscale
  # What the agent cannot install without sudo: a C toolchain, which Rust,
  # cgo, native modules, and source builds need, pkg-config, the half of
  # venv Ubuntu leaves out, and unzip for installers that insist on it.
  - build-essential
  - pkg-config
  - python3-venv
  - unzip
__CODEX_PACKAGES__
  # Rootless Docker for the agent user. slirp4netns gives it networking on
  # x86 and ARM alike; dbus-user-session lets it apply cgroup limits.
  - docker-ce
  - docker-ce-cli
  - containerd.io
  - docker-buildx-plugin
  - docker-compose-plugin
  - docker-ce-rootless-extras
  - uidmap
  - slirp4netns
  - dbus-user-session

# Docker's own repo ships the rootless extras and current releases, and
# Ubuntu's archive has no Tailscale package. Each key is pinned by
# fingerprint, fetched from the keyserver at boot, and trusted only for
# its repo.
apt:
  sources:
    docker:
      source: "deb [signed-by=$KEY_FILE] https://download.docker.com/linux/ubuntu $RELEASE stable"
      keyid: 9DC858229FC7DD38854AE2D88D81803C0EBFCD88
    tailscale:
      source: "deb [signed-by=$KEY_FILE] https://pkgs.tailscale.com/stable/ubuntu $RELEASE main"
      keyid: 2596A99EAAB33821893C0A79458CA832957F5868

# Hetzner's vendor-data sets a root password, allows root and password SSH
# login, and uses chpasswd syntax that makes cloud-init exit with warnings.
# None of it is wanted here, and without it any warning is a real problem.
vendor_data:
  enabled: false

# Hetzner turns on password login when no SSH key is passed to hcloud.
# OpenSSH is disabled below; this keeps passwords off should it ever return.
ssh_pwauth: false

# No authorized_keys: logins arrive over Tailscale SSH, which the tailnet
# policy decides by the connecting device's identity. Nothing on the box
# can log in to another user, because there is no key to do it with.
users:
  - name: __ADMIN_USER__
    groups: sudo
    sudo: ALL=(ALL) NOPASSWD:ALL
    shell: /bin/bash

  - name: __AGENT_USER__
    groups: users
    shell: /bin/bash

# write-files runs before users-groups, so this cannot target the home
# directory. runcmd appends it after the skel .bashrc exists. DOCKER_HOST is
# for tools that ignore the docker CLI context, such as Docker SDKs.
write_files:
  - path: /var/lib/buildbox/agent.bashrc
    permissions: "0644"
    content: |
      eval $(keychain --eval --agents ssh id_ed25519)
      export DOCKER_HOST=unix:///run/user/$(id -u)/docker.sock

runcmd:
  # Without --ssh-key, Hetzner sets a root password and prints/emails it.
  # Locking it makes those copies useless without asking for a Hetzner key.
  - ['passwd', '--lock', 'root']
  # Tailscale. Its package starts tailscaled, so up needs no service step.
  # The tag makes the box the tag's device rather than the user's, so no rule
  # about the user's devices applies to it; a key without the tag is refused
  # here. Auto-update keeps a long-lived box on a current client.
  - ['tailscale', 'up', '--auth-key=__TS_AUTHKEY__', '--ssh', '--advertise-tags=__TS_TAG__', '--hostname=__NAME__']
  - ['tailscale', 'set', '--auto-update']
  # Tailscale SSH answers port 22 on the tailnet and the firewall admits
  # nothing else, so OpenSSH would only ever serve loopback. Off.
  - ['systemctl', 'disable', '--now', 'ssh.socket', 'ssh.service']
  # Host-level lockdown
  - ['ufw', 'default', 'deny', 'incoming']
  - ['ufw', 'default', 'allow', 'outgoing']
  - ['ufw', 'allow', 'in', 'on', 'tailscale0']
  - ['ufw', '--force', 'enable']
  # Projects directory for agent
  - ['mkdir', '-p', '/home/__AGENT_USER__/projects']
  - ['chown', '__AGENT_USER__:__AGENT_USER__', '/home/__AGENT_USER__/projects']
  - ['sh', '-c', 'cat /var/lib/buildbox/agent.bashrc >> /home/__AGENT_USER__/.bashrc']
  - ['chown', '__AGENT_USER__:__AGENT_USER__', '/home/__AGENT_USER__/.bashrc']
  # Rootless Docker for the agent user. The packages start a root daemon;
  # stop it so the only Docker on the box runs as the agent user.
  - ['systemctl', 'disable', '--now', 'docker.service', 'docker.socket', 'containerd.service']
  - ['rm', '-f', '/var/run/docker.sock']
  # Linger keeps the agent's user manager, and its Docker daemon, running
  # without a login. The setup tool needs that manager to be up.
  - ['loginctl', 'enable-linger', '__AGENT_USER__']
  - ['sh', '-c', 'systemctl start user@$(id -u __AGENT_USER__).service']
`

func userDataDocument(toolLines string) string {
	rootless := "  - ['runuser', '-l', '__AGENT_USER__', '-c', " +
		"'XDG_RUNTIME_DIR=/run/user/$(id -u) dockerd-rootless-setuptool.sh install']\n"

	return userDataTemplate + rootless + toolLines
}

// Render is the user-data for a server called name. It fails when the
// config names an unknown coding tool.
func Render(cfg config.Config, name, authKey string) (string, error) {
	tools, err := config.ParseTools(cfg.AITools)
	if err != nil {
		return "", err
	}

	return strings.NewReplacer(
		"__AGENT_USER__", config.AgentUser,
		"__ADMIN_USER__", config.AdminUser,
		"__TS_AUTHKEY__", authKey,
		"__TS_TAG__", cfg.Tag,
		"__NAME__", name,
		"__CODEX_PACKAGES__", codexPackages(tools),
	).Replace(userDataDocument(codingToolLines(tools))), nil
}

// codexPackages is the apt entry for bubblewrap. Codex sandboxes its commands
// with /usr/bin/bwrap. The Ubuntu package is unprivileged, not setuid, so the
// agent user can run it without sudo.
func codexPackages(ids []string) string {
	if slices.Contains(ids, config.ToolCodex) {
		return "  - bubblewrap\n"
	}

	return ""
}

// codingToolLines are cloud-init runcmd entries. Each installer runs as the
// agent user, so the binary lands in that home directory rather than as root.
func codingToolLines(ids []string) string {
	chosen := map[string]bool{}
	for _, id := range ids {
		chosen[id] = true
	}

	var lines strings.Builder

	for _, tool := range config.Tools() {
		if !chosen[tool.ID] {
			continue
		}

		lines.WriteString("  # " + tool.Name + " for the agent user\n")
		lines.WriteString("  - ['su', '-', '__AGENT_USER__', '-c', '" + tool.Command + "']\n")
	}

	return lines.String()
}
