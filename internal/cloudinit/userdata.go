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
# A kernel from the upgrade only runs after a reboot, and a local root hole
# on the box is the agent user's. Ubuntu raises /run/reboot-required when a
# package needs one; the reboot lands here, before runcmd, so the box joins
# the tailnet on its final kernel. /run is empty again on the second boot.
package_reboot_if_required: true
# Hetzner's datasource refreshes the network config on every boot over the
# interface name it saw first, which netplan renames, so a second boot waits
# 30 s for a lease and fails. Nothing changes a box's network after it exists.
updates:
  network:
    when: [boot-new-instance]
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
# OpenSSH is removed below; this keeps passwords off should it ever return.
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
# for tools that ignore the docker CLI context, such as Docker SDKs. keychain
# is there for a Git key with a passphrase, to ask for it once; it is quiet
# so that a login shows the welcome below and not four lines about the agent
# it found. A box without a Git key has no such line.
write_files:
  - path: /var/lib/buildbox/agent.bashrc
    permissions: "0644"
    content: |
__KEYCHAIN__      export DOCKER_HOST=unix:///run/user/$(id -u)/docker.sock
  # Disabling OpenSSH's units does not keep it off. cloud-init's network
  # unit wants sshd.service, so every boot starts it again, and systemd 256
  # and later give an installed sshd a unix socket any local user can open.
  # These two empty files are the switches for both: Ubuntu's ssh units skip
  # sshd while the first exists, and the second masks the generator that
  # makes the socket. They also hold when a later package brings
  # openssh-server back as a dependency.
  - path: /etc/ssh/sshd_not_to_be_run
    permissions: "0644"
    content: ""
  - path: /etc/systemd/system-generators/systemd-ssh-generator
    permissions: "0644"
    content: ""
__WELCOME_FILES__
__ANTIGRAVITY_FILES__
runcmd:
  # Without --ssh-key, Hetzner sets a root password and prints/emails it.
  # Locking it makes those copies useless without asking for a Hetzner key.
  - ['passwd', '--lock', 'root']
  # Tailscale SSH is the only way in, so the box carries no OpenSSH server
  # for the agent user to reach on loopback. Remove, not purge: Tailscale SSH
  # presents the host keys in /etc/ssh, and a purge deletes them. Tailscale
  # serves SFTP itself, so OpenSSH's sftp-server goes too. Like every package
  # step, this comes before the tailnet join.
  - ['env', 'DEBIAN_FRONTEND=noninteractive', 'apt-get', '--option', 'DPkg::Lock::Timeout=300',
     'remove', '--yes', 'openssh-server', 'openssh-sftp-server']
  # systemd makes sshd's unix socket at the start of the first boot, before
  # the mask above exists. This stops what is left of it, so nothing
  # lingers on a box that did not reboot. A pattern and not the name: it
  # matches nothing where there is no such socket, which is no error.
  - ['systemctl', 'stop', 'sshd-*.socket']
  # Tailscale. Its package starts tailscaled, so up needs no service step.
  # The tag makes the box the tag's device rather than the user's, so no rule
  # about the user's devices applies to it; a key without the tag is refused
  # here. Auto-update keeps a long-lived box on a current client.
  - ['tailscale', 'up', '--auth-key=__TS_AUTHKEY__', '--ssh', '--advertise-tags=__TS_TAG__', '--hostname=__NAME__']
  - ['tailscale', 'set', '--auto-update']
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
  # Ubuntu greets a login with some thirty lines of links, load figures, and
  # notices. These files keep them from both users, whose welcome is the one
  # written above. admin's carries on the two that call for something: the
  # updates that wait, and a restart one of them needs.
  - ['runuser', '--user', '__AGENT_USER__', '--', 'touch', '/home/__AGENT_USER__/.hushlogin']
  - ['runuser', '--user', '__ADMIN_USER__', '--', 'touch', '/home/__ADMIN_USER__/.hushlogin']
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
		"__KEYCHAIN__", keychainLine(cfg),
		"__CODEX_PACKAGES__", codexPackages(tools),
		"__WELCOME_FILES__", welcomeFiles(tools, name),
		"__ANTIGRAVITY_FILES__", antigravityProfile(tools),
	).Replace(userDataDocument(codingToolLines(tools))), nil
}

// keychainLine loads the Git key into an ssh-agent at each interactive shell.
// Without a Git key it would only warn that the key is missing.
func keychainLine(cfg config.Config) string {
	if !cfg.SharesGitKey() {
		return ""
	}

	return "      eval $(keychain --quiet --eval --agents ssh " + config.GitKeyName + ")\n"
}

// antigravityProfilePath is agy's AppArmor profile, among the ones AppArmor
// loads at every boot.
const antigravityProfilePath = "/etc/apparmor.d/agy"

// antigravityProfile is the write_files entry for agy's AppArmor profile. The
// replacer does not read its own output, so the agent user is written in here
// rather than left as a placeholder.
func antigravityProfile(ids []string) string {
	if !slices.Contains(ids, config.ToolAntigravity) {
		return ""
	}

	return `  # Antigravity CLI builds its sandbox from user namespaces and offers Auto
  # mode only with it. Ubuntu denies working user namespaces to a program
  # without an AppArmor profile that allows them. This profile confines
  # nothing and allows only that, the shape Ubuntu ships for rootlesskit.
  # agy sits in the agent's home, so in effect the agent user gets them,
  # which rootlesskit already gives it. runcmd loads the profile.
  - path: ` + antigravityProfilePath + `
    permissions: "0644"
    content: |
      abi <abi/5.0>,
      include <tunables/global>

      profile agy /home/` + config.AgentUser + `/.local/bin/agy flags=(unconfined) {
        userns,
        @{exec_path} mr,
      }
`
}

// antigravityProfileLoad is the runcmd entry that loads the profile. It goes
// before agy's installer, so agy never runs without it.
const antigravityProfileLoad = `  # agy's AppArmor profile, before agy first runs. AppArmor reads
  # /etc/apparmor.d only at boot, so a box that did not reboot would not have
  # it. Replace, not add: a box that did reboot has it loaded already.
  - ['apparmor_parser', '--replace', '` + antigravityProfilePath + `']
`

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

		if tool.ID == config.ToolAntigravity {
			lines.WriteString(antigravityProfileLoad)
		}

		lines.WriteString("  # " + tool.Name + " for the agent user\n")
		lines.WriteString("  - ['su', '-', '__AGENT_USER__', '-c', '" + tool.Command + "']\n")
	}

	return lines.String()
}
