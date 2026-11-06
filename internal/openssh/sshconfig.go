// Package openssh is everything buildbox does with OpenSSH on this machine:
// the Host blocks and host keys in ~/.ssh/buildbox, the Include line in
// ~/.ssh/config, and running ssh, scp, and ssh-keygen.
package openssh

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/hkluke/buildbox/internal/abort"
)

// Modes for the directories and files buildbox writes under ~/.ssh.
const (
	DirPerm  fs.FileMode = 0o700
	FilePerm fs.FileMode = 0o600
)

// IncludeLine is the one line buildbox adds to ~/.ssh/config. It uses ~ so
// it reads the same in a synced dotfile on another machine.
const IncludeLine = "Include ~/.ssh/buildbox/config"

const (
	includeComment = "# Added by buildbox. Its hosts are in ~/.ssh/buildbox/config."
	includeBlock   = includeComment + "\n" + IncludeLine + "\n\n"
)

// Paths are the SSH files buildbox uses. It owns the two in
// ~/.ssh/buildbox. In the user's own files it adds one Include line, and
// delete still removes blocks that older versions appended there.
type Paths struct {
	UserConfig     string
	UserKnownHosts string
	BoxConfig      string
	BoxKnownHosts  string
}

// Place is a config file that may hold a buildbox Host block, with the
// known_hosts file that block's host key was recorded in.
type Place struct {
	Config     string
	KnownHosts string
}

// Host is a buildbox Host block and the address it points at.
type Host struct {
	Place   Place
	Address string
}

// NewPaths returns the SSH paths under the user's home directory.
func NewPaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, abort.Newf("Could not find the home directory.\n%s", err.Error())
	}

	return pathsIn(home), nil
}

func pathsIn(home string) Paths {
	dir := filepath.Join(home, ".ssh")
	box := filepath.Join(dir, "buildbox")

	return Paths{
		UserConfig:     filepath.Join(dir, "config"),
		UserKnownHosts: filepath.Join(dir, "known_hosts"),
		BoxConfig:      filepath.Join(box, "config"),
		BoxKnownHosts:  filepath.Join(box, "known_hosts"),
	}
}

// IsLegacy reports whether place is the user's own config, where older
// versions wrote Host blocks.
func (paths Paths) IsLegacy(place Place) bool {
	return place.Config == paths.UserConfig
}

// places lists buildbox's own file first. Boxes created before it existed
// have their block in the user's config instead.
func (paths Paths) places() []Place {
	return []Place{
		{Config: paths.BoxConfig, KnownHosts: paths.BoxKnownHosts},
		{Config: paths.UserConfig, KnownHosts: paths.UserKnownHosts},
	}
}

// FindHosts returns every place with an exact Host block for name.
func FindHosts(paths Paths, name string) ([]Host, error) {
	var found []Host

	for _, place := range paths.places() {
		payload, err := os.ReadFile(place.Config) // #nosec G304 -- one of the SSH config paths above.
		if os.IsNotExist(err) {
			continue
		}

		if err != nil {
			return nil, abort.Newf("Could not read %s.\n%s", place.Config, err.Error())
		}

		content := string(payload)
		if hasExactHost(content, name) {
			found = append(found, Host{Place: place, Address: sshConfigHostname(content, name)})
		}
	}

	return found, nil
}

// EnsureInclude puts the Include line at the top of the user's config,
// where it applies to every host and comes before the user's own Host *
// settings, which would otherwise win. A symlinked config belongs to a
// dotfile manager, so it is left alone. The second result says why the line
// was not added, for the caller to show with the line to add by hand.
func EnsureInclude(paths Paths) (bool, string) {
	info, err := os.Lstat(paths.UserConfig)

	switch {
	case os.IsNotExist(err):
		mkdirErr := os.MkdirAll(filepath.Dir(paths.UserConfig), DirPerm)
		if mkdirErr != nil {
			return false, "~/.ssh could not be created"
		}

		writeErr := os.WriteFile(paths.UserConfig, []byte(includeBlock), FilePerm)
		if writeErr != nil {
			return false, "it could not be created"
		}

		return true, ""
	case err != nil:
		return false, "it could not be read"
	case info.Mode()&fs.ModeSymlink != 0:
		return false, "it is a symlink"
	}

	payload, err := os.ReadFile(paths.UserConfig)
	if err != nil {
		return false, "it could not be read"
	}

	if hasInclude(string(payload), paths) {
		return false, ""
	}

	if replaceFile(paths.UserConfig, includeBlock+string(payload)) != nil {
		return false, "it could not be written"
	}

	return true, ""
}

// hasInclude accepts the forms ssh_config(5) allows for the same file: ~,
// absolute, or relative to ~/.ssh, with a space or = after the keyword.
func hasInclude(content string, paths Paths) bool {
	for _, line := range splitLines(content) {
		fields := strings.Fields(strings.Replace(strings.TrimSpace(line), "=", " ", 1))
		if len(fields) < 2 || !strings.EqualFold(fields[0], "Include") {
			continue
		}

		for _, arg := range fields[1:] {
			if includeTarget(arg, paths) == paths.BoxConfig {
				return true
			}
		}
	}

	return false
}

func includeTarget(arg string, paths Paths) string {
	arg = strings.Trim(arg, `"`)
	sshDir := filepath.Dir(paths.UserConfig)

	rest, found := strings.CutPrefix(arg, "~/")
	if found {
		return filepath.Join(filepath.Dir(sshDir), filepath.FromSlash(rest))
	}

	if filepath.IsAbs(arg) {
		return filepath.Clean(arg)
	}

	return filepath.Join(sshDir, filepath.FromSlash(arg))
}

func hasExactHost(content, name string) bool {
	return containsLine(content, "Host "+name)
}

func containsLine(content, want string) bool {
	return slices.Contains(splitLines(content), want)
}

func splitLines(content string) []string {
	if content == "" {
		return nil
	}

	parts := strings.Split(content, "\n")
	if strings.HasSuffix(content, "\n") {
		parts = parts[:len(parts)-1]
	}

	return parts
}

func isSSHComment(line string) bool {
	return regexp.MustCompile(`^[[:space:]]*#`).MatchString(line)
}

func isSSHBoundary(line string) bool {
	return regexp.MustCompile(`^[[:space:]]*(Host|Match|Include)([[:space:]]|$)`).MatchString(line)
}

// sshConfigHostname returns the first HostName in the block that starts at
// an exact "Host name" line. The block ends at the next Host, Match, or Include.
func sshConfigHostname(content, name string) string {
	hostLine := "Host " + name

	found := false
	for _, line := range splitLines(content) {
		if !found {
			if line == hostLine {
				found = true
			}

			continue
		}

		if isSSHBoundary(line) {
			return ""
		}

		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "HostName" {
			return fields[1]
		}
	}

	return ""
}

// rewriteSSHConfig drops the exact Host block. A blank line does not end the
// block: an unindented HostName left behind would apply to every host. One
// blank line directly above the removed Host line is dropped with it.
type sshRewrite struct {
	out          strings.Builder
	buffered     strings.Builder
	hostLine     string
	pendingBlank bool
	skipping     bool
}

func rewriteSSHConfig(content, name string) string {
	state := sshRewrite{hostLine: "Host " + name}
	for _, line := range splitLines(content) {
		state.push(line)
	}

	state.finish()

	return state.out.String()
}

func (state *sshRewrite) push(line string) {
	if state.skipping && state.keepSkipping(line) {
		return
	}

	state.pushKept(line)
}

func (state *sshRewrite) keepSkipping(line string) bool {
	if line == "" || isSSHComment(line) {
		state.buffered.WriteString(line)
		state.buffered.WriteByte('\n')

		return true
	}

	if isSSHBoundary(line) {
		state.out.WriteString(state.buffered.String())
		state.buffered.Reset()
		state.skipping = false

		return false
	}

	state.buffered.Reset()

	return true
}

func (state *sshRewrite) pushKept(line string) {
	if line == state.hostLine {
		state.pendingBlank = false
		state.skipping = true
		state.buffered.Reset()

		return
	}

	if line == "" {
		if state.pendingBlank {
			state.out.WriteByte('\n')
		}

		state.pendingBlank = true

		return
	}

	if state.pendingBlank {
		state.out.WriteByte('\n')
		state.pendingBlank = false
	}

	state.out.WriteString(line)
	state.out.WriteByte('\n')
}

func (state *sshRewrite) finish() {
	if state.skipping {
		state.out.WriteString(state.buffered.String())

		return
	}

	if state.pendingBlank {
		state.out.WriteByte('\n')
	}
}

// RemoveHost cuts the exact Host block for name out of the config at path.
func RemoveHost(path, name string) error {
	payload, err := os.ReadFile(path) // #nosec G304 -- path is an SSH config buildbox writes Host blocks to.
	if err != nil {
		return abort.Newf("Could not read %s.\n%s", path, err.Error())
	}

	updated := rewriteSSHConfig(string(payload), name)
	if containsLine(updated, "Host "+name) {
		return abort.Newf("Host %s is still in %s.", name, path)
	}

	return replaceFile(path, updated)
}

// replaceFile writes content through a temporary file and a rename, so a
// crash never leaves a half-written SSH config. It keeps the file's mode,
// and writes to a symlink's target so the link itself survives.
func replaceFile(path, content string) error {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		path = resolved
	}

	info, err := os.Stat(path)
	if err != nil {
		return abort.Newf("Could not read the mode of %s.\n%s", path, err.Error())
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".buildbox-sshconfig-*")
	if err != nil {
		return abort.Newf("Could not rewrite %s.\n%s", path, err.Error())
	}

	tmpName := tmp.Name()
	removeTmp := true

	defer func() {
		if removeTmp {
			_ = os.Remove(tmpName)
		}
	}()

	_, writeErr := tmp.WriteString(content)
	if writeErr != nil {
		_ = tmp.Close()

		return abort.Newf("Could not rewrite %s.\n%s", path, writeErr.Error())
	}

	closeErr := tmp.Close()
	if closeErr != nil {
		return abort.Newf("Could not rewrite %s.\n%s", path, closeErr.Error())
	}

	chmodErr := os.Chmod(tmpName, info.Mode().Perm())
	if chmodErr != nil {
		return abort.Newf("Could not read the mode of %s.\n%s", path, chmodErr.Error())
	}

	renameErr := os.Rename(tmpName, path)
	if renameErr != nil {
		return abort.Newf("Could not rewrite %s.\n%s", path, renameErr.Error())
	}

	removeTmp = false

	return nil
}

// hostBlock is the Host entry create appends. There is no IdentityFile:
// Tailscale SSH decides the login by the device's identity and ignores
// keys. UserKnownHostsFile keeps the box's host key out of the user's
// known_hosts, so delete never edits it. ForwardAgent no stops a Host *
// default from lending the laptop's SSH agent to the agent user; ssh -A
// still forwards for one session.
func hostBlock(name, address, user, knownHosts string) string {
	lines := []string{
		"",
		"Host " + name,
		"  HostName " + address,
		"  User " + user,
		"  UserKnownHostsFile " + knownHosts,
		"  ForwardAgent no",
		"  SetEnv TERM=xterm-256color",
		"",
	}

	return strings.Join(lines, "\n") + "\n"
}

// AppendHost adds the Host block for a new box to buildbox's own config.
func AppendHost(paths Paths, name, address, user string) error {
	path := paths.BoxConfig

	err := os.MkdirAll(filepath.Dir(path), DirPerm)
	if err != nil {
		return abort.Newf("Could not create %s.\n%s", filepath.Dir(path), err.Error())
	}
	// #nosec G304 -- path is buildbox's own SSH config.
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, FilePerm)
	if err != nil {
		return abort.Newf("Could not update %s.\n%s", path, err.Error())
	}
	defer func() { _ = file.Close() }()

	_, err = file.WriteString(hostBlock(name, address, user, Token(paths.BoxKnownHosts)))
	if err != nil {
		return abort.Newf("Could not update %s.\n%s", path, err.Error())
	}

	return nil
}

// KnownHostSafe guards the ssh-keygen call. The address comes from a file
// the user can edit, so anything but a plain host or IP is left alone.
func KnownHostSafe(host string) bool {
	return regexp.MustCompile(`^[A-Za-z0-9.:_-]+$`).MatchString(host)
}

// RemoveKnownHost reports whether file had an entry for host.
func RemoveKnownHost(ctx context.Context, host, file string) (bool, error) {
	if host == "" || !KnownHostSafe(host) {
		return false, nil
	}

	_, statErr := os.Stat(file)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return false, nil
		}

		return false, abort.Newf("Could not update %s for %s.\n%s", file, host, statErr.Error())
	}

	result, runErr := Run(ctx, "ssh-keygen", "-R", host, "-f", file)

	combined := result.Stdout + result.Stderr
	if runErr != nil {
		return false, abort.Newf("Could not update %s for %s.\n%s", file, host, strings.TrimSpace(combined))
	}

	return strings.Contains(combined, "updated"), nil
}

// Token is a path as an ssh_config value. OpenSSH expands a tilde, and a
// path with spaces is stored absolute and quoted.
func Token(absolute string) string {
	display := absolute

	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		rest, found := strings.CutPrefix(absolute, home+string(os.PathSeparator))
		if found {
			display = "~/" + filepath.ToSlash(rest)
		}
	}

	if strings.ContainsAny(display, " \t\"#") {
		return `"` + strings.ReplaceAll(absolute, `"`, `\"`) + `"`
	}

	return display
}
