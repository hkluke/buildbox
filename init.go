package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"golang.org/x/term"

	"github.com/hkluke/buildbox/internal/abort"
	"github.com/hkluke/buildbox/internal/config"
	"github.com/hkluke/buildbox/internal/console"
	"github.com/hkluke/buildbox/internal/hetzner"
	"github.com/hkluke/buildbox/internal/release"
	"github.com/hkluke/buildbox/internal/tailscale"
	"github.com/hkluke/buildbox/internal/wizard"
)

// The pages a new SSH key is pasted into, for the two hosts the Git key is
// configured for on the box.
const (
	gitHubKeysURL = "https://github.com/settings/ssh/new"
	gitLabKeysURL = "https://gitlab.com/-/user_settings/ssh_keys"
)

// initSetup is everything the form needs before it starts.
type initSetup struct {
	dest      string
	replacing bool
	seed      config.Config
	tailnet   tailscale.Tailnet
	client    *hcloud.Client
}

func runInit(ctx context.Context, args []string) error {
	err := screenInitArgs(args)
	if err != nil {
		return err
	}

	setup, err := openInit(ctx)
	if err != nil {
		return err
	}

	done, err := executeInit(ctx, setup)
	if err != nil {
		return err
	}

	return writeInit(ctx, done, setup.dest, setup.client)
}

func screenInitArgs(args []string) error {
	if helpRequested(args) {
		if len(args) != 1 {
			return abort.Usage("init does not take arguments", cmdInit)
		}

		return showCommandHelp(cmdInit)
	}

	if len(args) > 0 {
		return abort.Usage("init does not take arguments", cmdInit)
	}

	return nil
}

func openInit(ctx context.Context) (initSetup, error) {
	needs, err := checkInitNeeds(ctx)
	if err != nil {
		return initSetup{}, err
	}

	dest, replacing, err := initDestination()
	if err != nil {
		return initSetup{}, err
	}

	seed, err := initSeed(ctx, replacing, dest)
	if err != nil {
		return initSetup{}, err
	}

	return initSetup{dest: dest, replacing: replacing, seed: seed, tailnet: needs.tailnet, client: needs.client}, nil
}

// initNeeds is what init cannot start without.
type initNeeds struct {
	tailnet tailscale.Tailnet
	client  *hcloud.Client
}

// checkInitNeeds checks the terminal, Tailscale, and the Hetzner token
// together, so a first run learns everything it is missing at once rather
// than one rerun at a time. With all three in place it prints nothing.
func checkInitNeeds(ctx context.Context) (initNeeds, error) {
	terminal := term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))

	// The tailnet is read here: the policy step shows who this machine is,
	// and without Tailscale running there is nothing to set up for.
	tailnet, tailscaleErr := tailscale.Describe(ctx)
	client, hetznerErr := newHetznerClient()

	if terminal && tailscaleErr == nil && hetznerErr == nil {
		return initNeeds{tailnet: tailnet, client: client}, nil
	}

	con := console.New()

	showNeed(con, "Terminal", terminal, "interactive", "not a terminal")
	showNeed(con, "Tailscale", tailscaleErr == nil, "logged in to "+tailnet.Name, "not running or not logged in")
	showNeed(con, "Hetzner", hetznerErr == nil, "token found", "no token found")
	con.Blank()

	return initNeeds{}, initNeedsFailure(terminal, tailscaleErr, hetznerErr)
}

func showNeed(con *console.Console, label string, met bool, metDetail, missingDetail string) {
	if met {
		con.Done(label, metDetail)

		return
	}

	con.Failed(label, missingDetail)
}

// initNeedsFailure names what init is missing, each check's own reason, and
// how to fix each. The hint's indented lines are links.
func initNeedsFailure(terminal bool, tailscaleErr, hetznerErr error) error {
	var missing, reasons, fixes []string

	if !terminal {
		missing = append(missing, "a terminal")
		fixes = append(fixes, "Run it in a terminal. To write the config by hand instead, start from the example:\n"+
			abort.Indent+"https://github.com/hkluke/buildbox/blob/main/buildbox.toml.example")
	}

	if tailscaleErr != nil {
		missing = append(missing, "Tailscale")
		reasons = append(reasons, failureReason(tailscaleErr))
		fixes = append(fixes, "Start Tailscale on this machine and log in. If it is not installed yet:\n"+
			abort.Indent+"https://tailscale.com/download")
	}

	if hetznerErr != nil {
		missing = append(missing, "a Hetzner token")
		reasons = append(reasons, failureReason(hetznerErr))
		fixes = append(fixes, hetzner.TokenHint)
	}

	headline := "init needs " + console.JoinAnd(missing) + " first"

	return abort.Hint(headline, strings.Join(reasons, "\n"), strings.Join(fixes, "\n\n"))
}

// failureReason is the most specific line of err: an abort.Error's detail
// when it has one, such as "Tailscale state is Stopped.", or its headline.
func failureReason(err error) string {
	var failed *abort.Error
	if !errors.As(err, &failed) {
		return err.Error()
	}

	if detail, _, _ := strings.Cut(strings.TrimSpace(failed.Detail), "\n"); detail != "" {
		return detail
	}

	return failed.Headline + "."
}

func executeInit(ctx context.Context, setup initSetup) (wizard.Model, error) {
	form := wizard.New(setup.seed, setup.dest, setup.replacing, setup.tailnet, wizard.LoadCatalog(ctx, setup.client))
	if !release.Disabled() {
		form = form.WithUpdateCheck(wizard.UpdateCheck(ctx, buildVersion()))
	}

	program := tea.NewProgram(form, tea.WithAltScreen())

	final, err := program.Run()
	if err != nil {
		return wizard.Model{}, abort.Newf("Could not run init.\n%s", err.Error())
	}

	done, ok := final.(wizard.Model)
	if !ok {
		return wizard.Model{}, abort.New("Could not run init.")
	}

	if done.Fatal() != "" {
		return wizard.Model{}, abort.New(done.Fatal())
	}

	return done, nil
}

// writeInit writes the config before it creates the firewall, so a failed
// firewall never costs the answers. A config naming a firewall that is not
// there yet is safe: create refuses it and points back to init.
func writeInit(ctx context.Context, done wizard.Model, dest string, client *hcloud.Client) error {
	con := console.New()

	if !done.Confirmed() {
		con.Note("Left the config unchanged.")

		return nil
	}

	cfg := done.Config()

	err := cfg.RequireCreate()
	if err != nil {
		return err
	}

	err = config.Write(dest, cfg)
	if err != nil {
		return abort.Newf("Could not write %s.\n%s", dest, err.Error())
	}

	con.Done("Config", console.DisplayPath(dest))

	err = writeCredentials(con, config.CredentialsPath(dest), cfg)
	if err != nil {
		return err
	}

	public := done.NewGitPublicKey()

	if done.NeedsFirewall() {
		err = con.Run("Firewall", "creating "+hetzner.FirewallName, func() (string, error) {
			return hetzner.FirewallName + " created", hetzner.CreateFirewall(ctx, client)
		})
		if err != nil {
			return firewallFailure(err, cfg, public != "")
		}
	}

	if public != "" {
		showGitKey(ctx, con, public)
	}

	if !done.MintsKeys() {
		con.Blank()
		con.Note("create asks for a one-time Tailscale auth key each time, and shows where to get it.")
	}

	con.Blank()
	con.Commands([][2]string{
		{abort.Prog() + " create", "create a build box in " + cfg.ServerLoc + " on a " + cfg.ServerType},
	})

	return nil
}

// firewallFailure says the config is safe and how to finish. A Git key made
// in this run is named too, since a later run finds it on disk and does not
// show it again.
func firewallFailure(err error, cfg config.Config, newKey bool) error {
	hint := "The config is saved. Fix the cause, then run " + abort.Prog() +
		" init again and choose Write to create the firewall."

	if newKey {
		key, expandErr := config.ExpandPath(cfg.GitSSHKeyPath)
		if expandErr != nil {
			key = cfg.GitSSHKeyPath
		}

		hint += "\nThe new Git key's public half is in " + console.DisplayPath(key) + ".pub; add it to your git host."
	}

	return abort.WithHint(err, hint)
}

// writeCredentials keeps the OAuth client in its own file, so the config
// holds no secret. Without a client, a file from an earlier run goes away.
func writeCredentials(con *console.Console, path string, cfg config.Config) error {
	existed := config.FileExists(path)

	written, err := config.WriteCredentials(path, cfg)
	if err != nil {
		return abort.Newf("Could not write %s.\n%s", path, err.Error())
	}

	switch {
	case written:
		con.Done("Credentials", console.DisplayPath(path))
	case existed:
		con.Done("Credentials", console.DisplayPath(path)+" removed")
	}

	return nil
}

// showGitKey hands the new key over: on the clipboard when a clipboard tool
// is installed, and always on screen, with the pages it is pasted into.
func showGitKey(ctx context.Context, con *console.Console, public string) {
	lead := "Add the agent's Git key as a new SSH key:"
	if wizard.CopyToClipboard(ctx, public) {
		lead = "The agent's Git key is on the clipboard. Paste it as a new SSH key:"
	}

	con.Blank()
	con.Note(lead)
	con.Command(public)
	con.Blank()
	con.Commands([][2]string{
		{gitHubKeysURL, "GitHub"},
		{gitLabKeysURL, "GitLab"},
	})
}

func initDestination() (string, bool, error) {
	if path, ok := config.File(); ok {
		return path, true, nil
	}

	exe, err := os.Executable()
	if err != nil {
		return "", false, abort.Newf("Could not find this binary.\n%s", err.Error())
	}

	resolved, resolveErr := filepath.EvalSymlinks(exe)
	if resolveErr == nil {
		exe = resolved
	}

	if strings.Contains(exe, "go-build") {
		return config.ProjectName, false, nil
	}

	path, err := config.UserPath()
	if err != nil {
		return "", false, abort.Newf("Could not find the config directory.\n%s", err.Error())
	}

	return path, false, nil
}

func initSeed(ctx context.Context, exists bool, path string) (config.Config, error) {
	cfg := config.Config{}

	if exists {
		// Lenient on purpose: a file from an older buildbox may have keys that
		// no longer exist, and init is the command that replaces it.
		loaded, err := config.LoadSeed(path)
		if err != nil {
			return config.Config{}, err
		}

		cfg = loaded
	} else {
		// An earlier run may have saved its OAuth client and quit before
		// Write; reusing it saves creating another.
		saved, err := config.SeedCredentials(path, cfg)
		if err != nil {
			return config.Config{}, err
		}

		cfg = saved
	}

	return fillInitDefaults(ctx, config.FromEnvironment(cfg)), nil
}

func fillInitDefaults(ctx context.Context, cfg config.Config) config.Config {
	if cfg.Tag == "" {
		cfg.Tag = config.DefaultTag
	}

	// Without a dedicated key, init suggests generating one rather than
	// copying a personal key to the box.
	if cfg.GitSSHKeyPath == "" {
		cfg.GitSSHKeyPath = firstHomePath(wizard.GitKeyPath, "$HOME/.ssh/github_ed25519")
	}

	if cfg.GitUserName == "" {
		cfg.GitUserName = gitConfig(ctx, "user.name")
	}

	if cfg.GitUserEmail == "" {
		cfg.GitUserEmail = gitConfig(ctx, "user.email")
	}

	cfg = defaultCodingTools(cfg)

	if cfg.ServerImage == "" {
		cfg.ServerImage = config.DefaultImage
	}

	if cfg.ServerLoc == "" {
		cfg.ServerLoc = config.SeedLocation
	}

	if cfg.ServerType == "" {
		cfg.ServerType = config.DefaultType
	}

	return cfg
}

func defaultCodingTools(cfg config.Config) config.Config {
	if strings.TrimSpace(cfg.AITools) == "" {
		cfg.AITools = config.ToolOpenCode
	}

	return cfg
}

func firstHomePath(candidates ...string) string {
	for _, candidate := range candidates {
		expanded, err := config.ExpandPath(candidate)
		if err == nil && config.FileExists(expanded) {
			return candidate
		}
	}

	if len(candidates) == 0 {
		return ""
	}

	return candidates[0]
}

func gitConfig(ctx context.Context, key string) string {
	//nolint:gosec // G204: git is fixed and the key is user.name or user.email.
	cmd := exec.CommandContext(ctx, "git", "config", "--global", key)

	out, err := cmd.Output()
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(out))
}
