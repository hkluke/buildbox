package wizard

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/hkluke/buildbox/internal/config"
	"github.com/hkluke/buildbox/internal/console"
	"github.com/hkluke/buildbox/internal/openssh"
)

const (
	gitKeyComment = "buildbox git key"
	keygenFailed  = "Could not generate the key."
)

// GitKeyPath is where init generates the dedicated Git key.
const GitKeyPath = "$HOME/.ssh/buildbox_git_ed25519"

// displayKeyPath is a config path as the user would write it: $HOME expanded
// and then shortened to ~, or as it is when it cannot be expanded.
func displayKeyPath(configPath string) string {
	expanded, err := config.ExpandPath(configPath)
	if err != nil {
		return configPath
	}

	return console.DisplayPath(expanded)
}

func pathFree(configPath string) bool {
	expanded, err := config.ExpandPath(configPath)
	if err != nil {
		return false
	}

	return !config.FileExists(expanded)
}

// gitKeyTarget is the private config path of a new Git key, or empty when
// that key already exists. An existing key is never replaced.
func gitKeyTarget() string {
	if pathFree(GitKeyPath) && pathFree(GitKeyPath+".pub") {
		return GitKeyPath
	}

	return ""
}

// generateKey writes an ed25519 pair without a passphrase and returns the
// canonical private path. The public key is the file beside it with .pub.
func generateKey(privateConfig, comment string) (string, string) {
	private, err := config.ExpandPath(privateConfig)
	if err != nil {
		return "", config.PathExpandFailed
	}

	public := private + ".pub"
	if config.FileExists(private) || config.FileExists(public) {
		return "", "That key already exists."
	}

	_, lookErr := exec.LookPath("ssh-keygen")
	if lookErr != nil {
		return "", "ssh-keygen is not installed."
	}

	err = os.MkdirAll(filepath.Dir(private), openssh.DirPerm)
	if err != nil {
		return "", "Could not create " + filepath.Dir(private) + "."
	}

	args := []string{"-q", "-t", "ed25519", "-f", private, "-N", ""}
	if comment != "" {
		args = append(args, "-C", comment)
	}

	result, runErr := openssh.Run(context.Background(), "ssh-keygen", args...)
	if runErr != nil {
		removePartialKey(private, public)

		return "", keygenProblem(result.Stderr)
	}

	if !config.FileExists(public) {
		removePartialKey(private, public)

		return "", keygenFailed
	}

	return config.CanonicalPath(private)
}

// generatedGitKey is the Git key init just wrote: its canonical private path
// and the public key line to register with the git host.
type generatedGitKey struct {
	private string
	public  string
}

// generateGitKey writes the dedicated Git key. The comment becomes the key's
// title when the public half is pasted into GitHub.
func generateGitKey(privateConfig string) (generatedGitKey, string) {
	private, problem := generateKey(privateConfig, gitKeyComment)
	if problem != "" {
		return generatedGitKey{}, problem
	}

	key := generatedGitKey{private: private}

	expanded, err := config.ExpandPath(private + ".pub")
	if err != nil {
		return key, ""
	}

	payload, err := os.ReadFile(expanded) // #nosec G304 -- the public key ssh-keygen just wrote.
	if err != nil {
		return key, ""
	}

	key.public = strings.TrimSpace(string(payload))

	return key, ""
}

func removePartialKey(private, public string) {
	if config.FileExists(public) {
		return
	}

	_ = os.Remove(private)
}

func keygenProblem(stderr string) string {
	line := strings.TrimSpace(stderr)
	if line == "" {
		return keygenFailed
	}

	if index := strings.IndexByte(line, '\n'); index >= 0 {
		line = line[:index]
	}

	return "Could not generate the key. " + line
}
