// Package config is the buildbox config: where the file lives, how it is
// read and written, its defaults, and what a valid value is.
package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/hkluke/buildbox/internal/abort"
)

// ProjectName is the config file beside the binary or in a checkout.
const ProjectName = "buildbox.toml"

// ProjectCredentialsName is the credentials file beside a buildbox.toml.
//
//nolint:gosec // G101: the file's name, not a credential.
const ProjectCredentialsName = "buildbox.credentials.toml"

// Values init offers when the config does not set them.
const (
	DefaultImage = "ubuntu-26.04"
	SeedLocation = "fsn1"
	DefaultType  = "cx23"
	DefaultTag   = "tag:buildbox"
)

// ImagePrefix starts the name of every image buildbox can provision: the
// cloud-init document is written for Ubuntu, and any other image boots a
// server that never joins the tailnet. The release after the prefix is
// Hetzner's to judge.
const ImagePrefix = "ubuntu-"

// The two users on every box. Both are fixed: the tailnet policy's ssh rule
// names them, and a name that could change would leave every tailnet with a
// stale rule.
const (
	// AdminUser is the administrative login, with sudo.
	AdminUser = "admin"
	// AgentUser runs the coding agent, with rootless Docker and no sudo.
	AgentUser = "agent"
)

// The OAuth client can come from the environment instead of the file, for
// people who keep secrets in a manager.
const (
	EnvOAuthClientID = "TS_OAUTH_CLIENT_ID"
	//nolint:gosec // G101: this names the variable that holds the secret, not a secret.
	EnvOAuthClientSecret = "TS_OAUTH_CLIENT_SECRET"
)

const (
	userConfigName      = "config.toml"
	userCredentialsName = "credentials.toml"

	configKeyGitSSHKey    = "git.ssh_key"
	configKeyGitUserName  = "git.name"
	configKeyGitUserEmail = "git.email"
	configKeyFirewallName = "hetzner.firewall"
	configKeyServerType   = "hetzner.type"
	configKeyServerImage  = "hetzner.image"
	configKeyServerLoc    = "hetzner.location"
	configKeyTag          = "tailscale.tag"
)

// Config is every setting create needs. Paths are expanded once read.
type Config struct {
	GitSSHKeyPath     string
	GitUserName       string
	GitUserEmail      string
	FirewallName      string
	ServerType        string
	ServerImage       string
	ServerLoc         string
	AITools           string
	Tag               string
	OAuthClientID     string
	OAuthClientSecret string
}

// File is buildbox.toml beside this binary, or the per-user file from
// "make config". go run keeps its binary in a temporary directory, so a
// checkout's buildbox.toml is used then.
func File() (string, bool) {
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}

	resolved, resolveErr := filepath.EvalSymlinks(exe)
	if resolveErr == nil {
		exe = resolved
	}

	if strings.Contains(exe, "go-build") {
		if FileExists(ProjectName) {
			return ProjectName, true
		}
	} else {
		beside := filepath.Join(filepath.Dir(exe), ProjectName)
		if FileExists(beside) {
			return beside, true
		}
	}

	userFile, pathErr := UserPath()
	if pathErr != nil {
		return "", false
	}

	if FileExists(userFile) {
		return userFile, true
	}

	return "", false
}

// CredentialsPath is the credentials file that goes with the config at
// configPath: credentials.toml beside a config.toml, as Cargo and the AWS CLI
// keep theirs, and buildbox.credentials.toml beside a buildbox.toml, where a
// generic name could be another tool's file.
func CredentialsPath(configPath string) string {
	dir := filepath.Dir(configPath)
	if filepath.Base(configPath) == userConfigName {
		return filepath.Join(dir, userCredentialsName)
	}

	return filepath.Join(dir, ProjectCredentialsName)
}

// UserPath is the per-user config file, under $XDG_CONFIG_HOME when it is set.
func UserPath() (string, error) {
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("could not find the home directory: %w", err)
		}

		xdg = filepath.Join(home, ".config")
	}

	return filepath.Join(xdg, "buildbox", userConfigName), nil
}

// FileExists reports whether path is a file, not a directory.
func FileExists(path string) bool {
	info, err := os.Stat(path)

	return err == nil && !info.IsDir()
}

// Load reads the TOML config and the credentials file beside it. $HOME and
// ~/ in paths are expanded here. The files are the only source of settings,
// so a missing config points at init.
func Load() (Config, error) {
	path, ok := File()
	if !ok {
		return Config{}, missingConfig()
	}

	return LoadFile(path)
}

// missingConfig names the per-user file, which is where init writes unless
// the config sits beside the binary.
func missingConfig() error {
	detail := ""

	userFile, err := UserPath()
	if err == nil {
		detail = userFile + " does not exist."
	}

	return abort.Hint("No config file found", detail, "Write one with:\n"+abort.Indent+abort.Prog()+" init")
}

// RequireCreate fails on the first setting create cannot do without.
func (cfg Config) RequireCreate() error {
	required := []struct {
		key   string
		value string
	}{
		{configKeyGitSSHKey, cfg.GitSSHKeyPath},
		{configKeyGitUserName, cfg.GitUserName},
		{configKeyGitUserEmail, cfg.GitUserEmail},
		{configKeyFirewallName, cfg.FirewallName},
		{configKeyServerType, cfg.ServerType},
		{configKeyServerImage, cfg.ServerImage},
		{configKeyServerLoc, cfg.ServerLoc},
		{configKeyTag, cfg.Tag},
	}
	for _, item := range required {
		if item.value == "" {
			return abort.Hint(
				item.key+" is missing from the config",
				"",
				"Write a complete config with:\n"+abort.Indent+abort.Prog()+" init",
			)
		}
	}

	if problem := InvalidTag(cfg.Tag); problem != "" {
		return abort.New(configKeyTag + ": " + problem)
	}

	if problem := InvalidImage(cfg.ServerImage); problem != "" {
		return abort.Hint(
			configKeyServerImage+": "+strings.TrimSuffix(problem, "."),
			"",
			"Pick one with:\n"+abort.Indent+abort.Prog()+" init",
		)
	}

	_, err := ParseTools(cfg.AITools)
	if err != nil {
		return err
	}

	return nil
}

type tomlConfig struct {
	Git       tomlGit       `toml:"git"`
	Hetzner   tomlHetzner   `toml:"hetzner"`
	Tools     *tomlTools    `toml:"tools"`
	Tailscale tomlTailscale `toml:"tailscale"`
}

type tomlGit struct {
	SSHKey string `toml:"ssh_key"`
	Name   string `toml:"name"`
	Email  string `toml:"email"`
}

type tomlHetzner struct {
	Firewall string `toml:"firewall"`
	Type     string `toml:"type"`
	Image    string `toml:"image"`
	Location string `toml:"location"`
}

type tomlTailscale struct {
	Tag string `toml:"tag"`
}

type tomlTools struct {
	Grok     bool `toml:"grok"`
	Claude   bool `toml:"claude"`
	Codex    bool `toml:"codex"`
	OpenCode bool `toml:"opencode"`
}

// tomlCredentials is the credentials file: the OAuth client alone, so the
// config can be shared and the secret cannot.
type tomlCredentials struct {
	Tailscale tomlCredentialsTailscale `toml:"tailscale"`
}

type tomlCredentialsTailscale struct {
	OAuthClientID     string `toml:"oauth_client_id"`
	OAuthClientSecret string `toml:"oauth_client_secret"`
}

// LoadFile reads the config at path and the credentials file beside it, and
// rejects keys it does not know, so a typo fails loudly instead of being
// ignored.
func LoadFile(path string) (Config, error) {
	return load(path, true)
}

// LoadSeed reads both files for init, keeping the settings it still knows
// and dropping the rest. A file written by an older buildbox can be
// rewritten rather than refused.
func LoadSeed(path string) (Config, error) {
	return load(path, false)
}

func load(path string, strict bool) (Config, error) {
	var decoded tomlConfig

	err := decodeFile(path, strict, &decoded)
	if err != nil {
		return Config{}, err
	}

	cfg, err := readCredentials(CredentialsPath(path), strict, configFromTOML(decoded))
	if err != nil {
		return Config{}, err
	}

	return expandConfigPaths(FromEnvironment(withDefaults(cfg)))
}

// SeedCredentials adds the OAuth client saved beside configPath to cfg, for
// init. init saves the client the moment it exists, so the credentials file
// can be there before any config is.
func SeedCredentials(configPath string, cfg Config) (Config, error) {
	return readCredentials(CredentialsPath(configPath), false, cfg)
}

// readCredentials adds the OAuth client from the credentials file, when
// there is one.
func readCredentials(path string, strict bool, cfg Config) (Config, error) {
	if !FileExists(path) {
		return cfg, nil
	}

	var decoded tomlCredentials

	err := decodeFile(path, strict, &decoded)
	if err != nil {
		return Config{}, err
	}

	cfg.OAuthClientID = decoded.Tailscale.OAuthClientID
	cfg.OAuthClientSecret = decoded.Tailscale.OAuthClientSecret

	return cfg, nil
}

func decodeFile(path string, strict bool, into any) error {
	raw, err := os.ReadFile(path) // #nosec G304 -- the path is a config file chosen by File or init.
	if err != nil {
		return abort.Newf("Could not read %s.\n%s", path, err.Error())
	}

	decoder := toml.NewDecoder(bytes.NewReader(raw))
	if strict {
		decoder.DisallowUnknownFields()
	}

	err = decoder.Decode(into)
	if err != nil {
		return abort.Hint(
			"Could not read "+path,
			err.Error(),
			"Unknown keys are an error, so a file from an older buildbox may need rewriting:\n"+
				abort.Indent+abort.Prog()+" init",
		)
	}

	return nil
}

func configFromTOML(decoded tomlConfig) Config {
	return Config{
		GitSSHKeyPath: decoded.Git.SSHKey,
		GitUserName:   decoded.Git.Name,
		GitUserEmail:  decoded.Git.Email,
		FirewallName:  decoded.Hetzner.Firewall,
		ServerType:    decoded.Hetzner.Type,
		ServerImage:   decoded.Hetzner.Image,
		ServerLoc:     decoded.Hetzner.Location,
		AITools:       codingToolsFromTable(decoded.Tools),
		Tag:           decoded.Tailscale.Tag,
	}
}

// withDefaults fills the one setting with a documented default. Everything
// else create needs is written by init and required.
func withDefaults(cfg Config) Config {
	if strings.TrimSpace(cfg.Tag) == "" {
		cfg.Tag = DefaultTag
	}

	return cfg
}

// FromEnvironment lets the OAuth client live in a secret manager rather
// than the file. Either variable alone is an incomplete client, and
// create then falls back to asking for a key.
func FromEnvironment(cfg Config) Config {
	if value := os.Getenv(EnvOAuthClientID); value != "" {
		cfg.OAuthClientID = value
	}

	if value := os.Getenv(EnvOAuthClientSecret); value != "" {
		cfg.OAuthClientSecret = value
	}

	return cfg
}

func expandConfigPaths(cfg Config) (Config, error) {
	var err error

	cfg.GitSSHKeyPath, err = expandOptionalPath(cfg.GitSSHKeyPath)
	if err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func expandOptionalPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", nil
	}

	expanded, err := ExpandPath(path)
	if err != nil {
		return "", abort.New(PathExpandFailed)
	}

	return expanded, nil
}
