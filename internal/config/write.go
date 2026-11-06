package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ConfigPerm is for the config, which holds no secret and can be shared.
const ConfigPerm = 0o644

// CredentialsPerm keeps the OAuth client secret private to the user.
const CredentialsPerm = 0o600

const configDirPerm = 0o700

// Render is the config file for cfg, with a comment on each section.
func Render(cfg Config) string {
	lines := []string{
		"# buildbox config. Rewrite it with: buildbox init",
		"",
		"# Boxes join the tailnet with this tag, and the tailnet policy names it.",
		"# The OAuth client that mints their auth keys is in the credentials file",
		"# beside this one; without one, create asks for a key.",
		"[tailscale]",
		"tag = " + tomlBasicString(cfg.Tag),
		"",
		"# Git identity, and the key copied to the agent after boot.",
		"# $HOME and ~/ are expanded when the file is read.",
		"[git]",
		"ssh_key = " + tomlBasicString(cfg.GitSSHKeyPath),
		"name = " + tomlBasicString(cfg.GitUserName),
		"email = " + tomlBasicString(cfg.GitUserEmail),
		"",
		"# The Hetzner server.",
		"[hetzner]",
		"firewall = " + tomlBasicString(cfg.FirewallName),
		"type = " + tomlBasicString(cfg.ServerType),
		"image = " + tomlBasicString(cfg.ServerImage),
		"location = " + tomlBasicString(cfg.ServerLoc),
		"",
		"# Coding tools installed for the agent user.",
		"[tools]",
		"opencode = " + tomlBool(ToolEnabled(cfg.AITools, ToolOpenCode)),
		"claude = " + tomlBool(ToolEnabled(cfg.AITools, ToolClaude)),
		"grok = " + tomlBool(ToolEnabled(cfg.AITools, ToolGrok)),
		"codex = " + tomlBool(ToolEnabled(cfg.AITools, ToolCodex)),
	}

	return strings.Join(lines, "\n") + "\n"
}

// RenderCredentials is the credentials file for cfg: the OAuth client alone.
func RenderCredentials(cfg Config) string {
	return strings.Join([]string{
		"# buildbox credentials. Rewrite it with: buildbox init",
		"",
		"# The OAuth client create mints one-time auth keys with. It can only mint",
		"# keys for the tag in the config; it cannot read devices or change the",
		"# policy. Replace it from the Tailnet section of init.",
		"[tailscale]",
		"oauth_client_id = " + tomlBasicString(cfg.OAuthClientID),
		"oauth_client_secret = " + tomlBasicString(cfg.OAuthClientSecret),
	}, "\n") + "\n"
}

func tomlBasicString(value string) string {
	var builder strings.Builder

	builder.WriteByte('"')

	for _, char := range value {
		switch char {
		case '\\', '"':
			builder.WriteByte('\\')
			builder.WriteRune(char)
		case '\n':
			builder.WriteString(`\n`)
		case '\r':
			builder.WriteString(`\r`)
		case '\t':
			builder.WriteString(`\t`)
		default:
			builder.WriteRune(char)
		}
	}

	builder.WriteByte('"')

	return builder.String()
}

func tomlBool(value bool) string {
	if value {
		return "true"
	}

	return "false"
}

// Write replaces the config at path with cfg, without the OAuth client.
func Write(path string, cfg Config) error {
	return writeFile(path, Render(cfg), ConfigPerm)
}

// WriteCredentials replaces the credentials file at path with cfg's OAuth
// client, or removes the file when cfg has none, so a client the user gave
// up is not read again by the next create. It reports whether a file was
// written.
func WriteCredentials(path string, cfg Config) (bool, error) {
	if cfg.OAuthClientID == "" && cfg.OAuthClientSecret == "" {
		err := os.Remove(path)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return false, fmt.Errorf("remove credentials file: %w", err)
		}

		return false, nil
	}

	return true, writeFile(path, RenderCredentials(cfg), CredentialsPerm)
}

// writeFile writes a temporary file and renames it, so a failed write never
// leaves half a file behind.
func writeFile(path, body string, perm os.FileMode) error {
	dir := filepath.Dir(path)

	err := os.MkdirAll(dir, configDirPerm)
	if err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	temp, err := os.CreateTemp(dir, ".buildbox-*")
	if err != nil {
		return fmt.Errorf("create config file: %w", err)
	}

	name := temp.Name()

	err = writeTemp(temp, body, perm)
	if err != nil {
		_ = os.Remove(name)

		return err
	}

	err = os.Rename(name, path)
	if err != nil {
		_ = os.Remove(name)

		return fmt.Errorf("replace config file: %w", err)
	}

	return nil
}

func writeTemp(file *os.File, body string, perm os.FileMode) error {
	err := file.Chmod(perm)
	if err != nil {
		_ = file.Close()

		return fmt.Errorf("set config permissions: %w", err)
	}

	_, err = file.WriteString(body)
	if err != nil {
		_ = file.Close()

		return fmt.Errorf("write config: %w", err)
	}

	err = file.Close()
	if err != nil {
		return fmt.Errorf("close config: %w", err)
	}

	return nil
}
