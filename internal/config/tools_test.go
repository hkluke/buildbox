package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseCodingTools(t *testing.T) {
	t.Parallel()

	got, err := ParseTools("")
	if err != nil || len(got) != 1 || got[0] != ToolOpenCode {
		t.Fatalf("empty = %v %v", got, err)
	}

	got, err = ParseTools(ToolNone)
	if err != nil || len(got) != 0 {
		t.Fatalf("none = %v %v", got, err)
	}

	got, err = ParseTools("Claude " + ToolGrok)
	if err != nil || strings.Join(got, " ") != ToolClaude+" "+ToolGrok {
		t.Fatalf("order = %v %v", got, err)
	}

	_, err = ParseTools(ToolNone + " " + ToolGrok)
	if err == nil {
		t.Fatal("mixed none")
	}

	_, err = ParseTools("nope")
	if err == nil {
		t.Fatal("unknown")
	}
}

func TestToggleCodingTool(t *testing.T) {
	t.Parallel()

	if ToggleTool(ToolGrok, ToolGrok) != ToolNone {
		t.Fatal("clear grok")
	}

	if ToggleTool(ToolNone, ToolClaude) != ToolClaude {
		t.Fatal("add claude")
	}

	got := ToggleTool(ToolGrok, ToolOpenCode)
	if got != ToolOpenCode+" "+ToolGrok {
		t.Fatalf("got %q", got)
	}
}

func TestRenderConfigWritesCodingTools(t *testing.T) {
	t.Parallel()

	body := Render(Config{AITools: ToolNone})
	if !strings.Contains(body, "grok = false") || !strings.Contains(body, "claude = false") {
		t.Fatalf("body:\n%s", body)
	}

	body = Render(Config{})
	if !strings.Contains(body, "opencode = true") || !strings.Contains(body, "grok = false") {
		t.Fatalf("default body:\n%s", body)
	}
}

func TestLoadConfigExpandsPathsAndTools(t *testing.T) {
	t.Parallel()

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), userConfigName)

	err = os.WriteFile(path, []byte(`
[tailscale]
tag = "tag:dev"

[git]
ssh_key = "~/keys/git"

[tools]
grok = false
claude = true
`), ConfigPerm)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.GitSSHKeyPath != filepath.Join(home, "keys", "git") {
		t.Fatalf("git path %q", cfg.GitSSHKeyPath)
	}

	if cfg.Tag != "tag:dev" {
		t.Fatalf("tag %q", cfg.Tag)
	}

	if cfg.AITools != ToolClaude {
		t.Fatalf("tools %q", cfg.AITools)
	}

	err = os.WriteFile(path, []byte("not = true\n"), ConfigPerm)
	if err != nil {
		t.Fatal(err)
	}

	_, err = LoadFile(path)
	if err == nil {
		t.Fatal("unknown field")
	}
}

// sampleClientSecret stands in for an OAuth client secret.
//
//nolint:gosec // G101: a made-up value for the test.
const sampleClientSecret = "tskey-client-test"

func TestRenderConfigQuotesAndWritesTheTailscaleSection(t *testing.T) {
	t.Parallel()

	body := Render(Config{
		GitSSHKeyPath:     `$HOME/keys/quote"key`,
		GitUserName:       "Ada Lovelace",
		GitUserEmail:      "ada@example.com",
		FirewallName:      "buildbox-tailscale-only",
		ServerType:        DefaultType,
		ServerImage:       DefaultImage,
		ServerLoc:         SeedLocation,
		Tag:               DefaultTag,
		OAuthClientID:     "k123",
		OAuthClientSecret: sampleClientSecret,
	})

	for _, want := range []string{
		`tag = "` + DefaultTag + `"`,
		`ssh_key = "$HOME/keys/quote\"key"`,
		`name = "Ada Lovelace"`,
		`location = "` + SeedLocation + `"`,
		"opencode = true",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %s\n%s", want, body)
		}
	}

	if strings.Contains(body, "[ssh]") || strings.Contains(body, "auth_key =") || strings.Contains(body, "oauth") {
		t.Fatalf("login key, auth key, or OAuth client in the config:\n%s", body)
	}

	creds := RenderCredentials(Config{OAuthClientID: "k123", OAuthClientSecret: sampleClientSecret})
	for _, want := range []string{
		"[tailscale]",
		`oauth_client_id = "k123"`,
		`oauth_client_secret = "` + sampleClientSecret + `"`,
	} {
		if !strings.Contains(creds, want) {
			t.Fatalf("missing %s\n%s", want, creds)
		}
	}

	if strings.Contains(creds, "tag =") {
		t.Fatalf("the tag is not a credential:\n%s", creds)
	}
}

func TestLoadFileReadsTheCredentialsBesideTheConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, userConfigName)
	creds := CredentialsPath(path)

	if creds != filepath.Join(dir, userCredentialsName) {
		t.Fatalf("credentials path = %q", creds)
	}

	writeTestFile(t, path, "[git]\nname = \"Ada\"\n")
	writeTestFile(t, creds, "[tailscale]\noauth_client_id = \"k9\"\noauth_client_secret = \""+sampleClientSecret+"\"\n")

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.OAuthClientID != "k9" || cfg.OAuthClientSecret != sampleClientSecret || cfg.Tag != DefaultTag {
		t.Fatalf("cfg = %+v", cfg)
	}

	writeTestFile(t, creds, "[tailscale]\ntag = \"tag:elsewhere\"\n")

	_, err = LoadFile(path)
	if err == nil {
		t.Fatal("the tag does not belong in the credentials file")
	}

	writeTestFile(t, path, "[tailscale]\noauth_client_id = \"k9\"\n")

	_, err = LoadFile(path)
	if err == nil {
		t.Fatal("the client does not belong in the config")
	}
}

func TestWriteCredentialsIsPrivateAndRemovesAnEmptyClient(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, userConfigName)
	creds := CredentialsPath(path)

	err := Write(path, Config{Tag: DefaultTag})
	if err != nil {
		t.Fatal(err)
	}

	assertPerm(t, path, ConfigPerm)

	written, err := WriteCredentials(creds, Config{OAuthClientID: "k1", OAuthClientSecret: sampleClientSecret})
	if err != nil || !written {
		t.Fatalf("written %v err %v", written, err)
	}

	assertPerm(t, creds, CredentialsPerm)

	written, err = WriteCredentials(creds, Config{})
	if err != nil || written || FileExists(creds) {
		t.Fatalf("written %v err %v exists %v", written, err, FileExists(creds))
	}

	// Removing what is not there is not a failure.
	_, err = WriteCredentials(creds, Config{})
	if err != nil {
		t.Fatal(err)
	}
}

func assertPerm(t *testing.T, path string, want os.FileMode) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if info.Mode().Perm() != want {
		t.Fatalf("%s mode = %o, want %o", path, info.Mode().Perm(), want)
	}
}

func TestLoadConfigDefaultsTheTagAndReadsTheOAuthClientFromEnv(t *testing.T) {
	t.Setenv(EnvOAuthClientID, "env-id")
	t.Setenv(EnvOAuthClientSecret, "env-secret")

	path := filepath.Join(t.TempDir(), userConfigName)

	err := os.WriteFile(path, []byte("[git]\nname = \"Ada\"\n"), ConfigPerm)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Tag != DefaultTag || cfg.OAuthClientID != "env-id" || cfg.OAuthClientSecret != "env-secret" {
		t.Fatalf("cfg = %+v", cfg)
	}

	err = os.WriteFile(path, []byte("[ssh]\npubkey = \"x\"\n"), ConfigPerm)
	if err != nil {
		t.Fatal(err)
	}

	_, err = LoadFile(path)
	if err == nil {
		t.Fatal("the removed ssh section should be an unknown field")
	}
}

func TestCanonicalConfigPathUsesHome(t *testing.T) {
	t.Parallel()

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	got, problem := CanonicalPath(filepath.Join(home, ".ssh", "id_ed25519.pub"))
	if problem != "" || got != "$HOME/.ssh/id_ed25519.pub" {
		t.Fatalf("got %q problem %q", got, problem)
	}

	got, problem = CanonicalPath("~/keys/git")
	if problem != "" || got != "$HOME/keys/git" {
		t.Fatalf("tilde got %q problem %q", got, problem)
	}
}

func TestValueValidation(t *testing.T) {
	t.Parallel()

	if InvalidEmail("ada@example.com") != "" || InvalidEmail("ada") == "" {
		t.Fatal("email")
	}

	if InvalidPersonName("  ") == "" {
		t.Fatal("name")
	}
}

func TestLoadNeedsAConfigFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "No config file found") {
		t.Fatalf("err = %v", err)
	}

	if !strings.Contains(err.Error(), "init") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadSeedKeepsWhatItKnowsFromAnOlderFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), userConfigName)

	err := os.WriteFile(path, []byte(`
[ssh]
pubkey = "$HOME/.ssh/id_ed25519.pub"

[git]
name = "Ada Lovelace"

[tailscale]
auth_key = "tskey-auth-old"
`), ConfigPerm)
	if err != nil {
		t.Fatal(err)
	}

	_, err = LoadFile(path)
	if err == nil {
		t.Fatal("create should still refuse the old keys")
	}

	seed, err := LoadSeed(path)
	if err != nil {
		t.Fatal(err)
	}

	if seed.GitUserName != "Ada Lovelace" || seed.Tag != DefaultTag {
		t.Fatalf("seed = %+v", seed)
	}

	_, err = LoadSeed(filepath.Join(t.TempDir(), "missing.toml"))
	if err == nil {
		t.Fatal("a missing file is still an error")
	}
}
