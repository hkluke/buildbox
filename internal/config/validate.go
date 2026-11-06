package config

import (
	"os"
	"regexp"
	"strings"
)

// Each Invalid function returns the problem to show the user, or empty when
// the value is fine.

const keyPeekBytes = 80

var (
	emailPattern = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)
	tagPattern   = regexp.MustCompile(`^tag:[a-z0-9][a-z0-9-]*$`)
)

// InvalidPrivateKey checks that path is a readable private key, not a
// public key.
func InvalidPrivateKey(path string) string {
	if strings.TrimSpace(path) == "" {
		return "Git SSH key path is required."
	}

	expanded, err := ExpandPath(path)
	if err != nil {
		return PathExpandFailed
	}

	info, err := os.Stat(expanded) // #nosec G703 -- the path is the one the user is choosing.
	if err != nil || info.IsDir() || info.Size() == 0 {
		return "Could not read " + expanded + "."
	}

	if publicKeyFile(expanded) {
		return "That file is a public key. Choose the private key."
	}

	return ""
}

func publicKeyFile(path string) bool {
	file, err := os.Open(path) // #nosec G304 -- the path is the one the user is choosing.
	if err != nil {
		return false
	}

	defer func() { _ = file.Close() }()

	buf := make([]byte, keyPeekBytes)

	n, err := file.Read(buf)
	if err != nil || n == 0 {
		return false
	}

	head := strings.TrimSpace(string(buf[:n]))

	return strings.HasPrefix(head, "ssh-") || strings.HasPrefix(head, "ecdsa-") || strings.HasPrefix(head, "sk-")
}

// InvalidPersonName checks a Git user name.
func InvalidPersonName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return "Git name is required."
	}

	return ""
}

// InvalidEmail checks a Git email address.
func InvalidEmail(value string) string {
	if !emailPattern.MatchString(strings.TrimSpace(value)) {
		return "Git email needs an @ and a domain."
	}

	return ""
}

// InvalidTag checks a Tailscale ACL tag, the identity every box joins with.
func InvalidTag(value string) string {
	if !tagPattern.MatchString(strings.TrimSpace(value)) {
		return "Use tag:name, with lowercase letters, digits, and hyphens."
	}

	return ""
}

// InvalidImage checks an image name against ImagePrefix.
func InvalidImage(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, " \t\r\n") || !strings.HasPrefix(value, ImagePrefix) {
		return "Use an Ubuntu image, such as " + DefaultImage + ". Boxes are set up for Ubuntu."
	}

	return ""
}

// InvalidToken checks a single-word secret; label names it in the message.
func InvalidToken(label, value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, " \t\r\n") {
		return label + " is required."
	}

	return ""
}
