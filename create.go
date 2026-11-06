package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"golang.org/x/term"

	"github.com/hkluke/buildbox/internal/abort"
	"github.com/hkluke/buildbox/internal/cloudinit"
	"github.com/hkluke/buildbox/internal/config"
	"github.com/hkluke/buildbox/internal/console"
	"github.com/hkluke/buildbox/internal/hetzner"
	"github.com/hkluke/buildbox/internal/openssh"
	"github.com/hkluke/buildbox/internal/tailscale"
)

const (
	nameMaxLength      = 63
	sshConnectTimeout  = 5
	sshWaitAttempts    = 30
	sshWaitGap         = 2 * time.Second
	policyWaitAttempts = 60
	policyWaitGap      = 5 * time.Second
	authKeyLabel       = "Auth key"
	authFromEnv        = "from TS_AUTHKEY"
	authFromPrompt     = "entered"
	cursorUpClear      = "\033[1A\r\033[K"
	// policyBanner is what Tailscale SSH says when the tailnet policy has no
	// rule for the login. More attempts would not change the policy.
	policyBanner = "tailnet policy does not permit"
)

// errPolicyDenied means the box is up and the tailnet policy has no rule
// that lets this device log in. create then shows the rule and waits for it.
var errPolicyDenied = errors.New("the tailnet policy refuses the login")

// errSSHTimeout means the box never answered on port 22 within the attempts.
var errSSHTimeout = errors.New("ssh did not answer in time")

type createPlan struct {
	name    string
	cfg     config.Config
	client  *hcloud.Client
	con     *console.Console
	ssh     openssh.Paths
	authKey string
}

// stage is one step of create after the server exists.
type stage struct {
	label   string
	running string
	action  func() (string, error)
}

func runCreate(ctx context.Context, args []string) (err error) {
	var (
		plan    createPlan
		created bool
	)

	defer func() {
		err = createFailure(ctx, err, created, plan.name)
	}()

	plan, err = prepareCreate(ctx, args)
	if err != nil {
		return err
	}

	userData, err := cloudinit.Render(plan.cfg, plan.name, plan.authKey)
	if err != nil {
		return err
	}

	// An interrupt during the auth key prompt is only seen once the prompt
	// returns. Nothing exists yet, so stop before creating anything.
	if ctx.Err() != nil {
		return interrupted()
	}

	started := time.Now()

	err = plan.con.Run("Server", "creating", func() (string, error) {
		var createErr error

		created, createErr = hetzner.CreateServer(ctx, plan.client, plan.cfg, plan.name, userData)

		return "created", createErr
	})
	if err != nil {
		return err
	}

	return finishCreate(ctx, plan, started)
}

func prepareCreate(ctx context.Context, args []string) (createPlan, error) {
	name, cfg, err := loadCreateConfig(args)
	if err != nil {
		return createPlan{}, err
	}

	paths, err := openssh.NewPaths()
	if err != nil {
		return createPlan{}, err
	}

	// The box's host key is recorded on the first ssh call, before its Host
	// entry exists. ssh does not create the directory for it.
	err = os.MkdirAll(filepath.Dir(paths.BoxKnownHosts), openssh.DirPerm)
	if err != nil {
		return createPlan{}, abort.Newf("Could not create %s.\n%s", filepath.Dir(paths.BoxKnownHosts), err.Error())
	}

	con := console.New()
	con.Header(cmdCreate, name, cfg.ServerType, cfg.ServerLoc, cfg.ServerImage)

	plan, err := authorizeCreate(ctx, con, name, cfg, paths)
	plan.ssh = paths

	return plan, err
}

func loadCreateConfig(args []string) (string, config.Config, error) {
	name, err := parseCreateArgs(args)
	if err != nil {
		return "", config.Config{}, err
	}

	err = validateName(name)
	if err != nil {
		return "", config.Config{}, err
	}

	cfg, err := config.Load()
	if err != nil {
		return "", config.Config{}, err
	}

	err = cfg.RequireCreate()
	if err != nil {
		return "", config.Config{}, err
	}

	err = openssh.Require("ssh", "scp")
	if err != nil {
		return "", config.Config{}, err
	}

	return name, cfg, nil
}

func authorizeCreate(
	ctx context.Context,
	con *console.Console,
	name string,
	cfg config.Config,
	paths openssh.Paths,
) (createPlan, error) {
	err := rejectExistingName(ctx, name, paths)
	if err != nil {
		return createPlan{}, err
	}

	// The Git key is copied to the box after boot. It is checked here, before
	// any Hetzner call, so a bad path is reported without one.
	problem := config.InvalidPrivateKey(cfg.GitSSHKeyPath)
	if problem != "" {
		return createPlan{}, abort.New("git.ssh_key: " + problem)
	}

	client, err := newHetznerClient()
	if err != nil {
		return createPlan{}, err
	}

	err = con.Run("Firewall", "checking "+cfg.FirewallName, func() (string, error) {
		return cfg.FirewallName, hetzner.AssertFirewall(ctx, client, cfg.FirewallName)
	})
	if err != nil {
		return createPlan{}, err
	}

	authKey, err := resolveAuthKey(ctx, con, cfg)
	if err != nil {
		return createPlan{}, err
	}

	return createPlan{name: name, cfg: cfg, client: client, con: con, authKey: authKey}, nil
}

func finishCreate(ctx context.Context, plan createPlan, started time.Time) error {
	var address string

	err := plan.con.Run("Tailnet", fmt.Sprintf("joining, up to %dm", tailscale.WaitMinutes()), func() (string, error) {
		joined, joinErr := tailscale.Wait(ctx, plan.name)
		if joinErr != nil {
			return "", joinErr
		}

		address = joined.Address

		return address, requireTag(plan.name, plan.cfg.Tag, joined.Tags)
	})
	if err != nil {
		return err
	}

	err = connectSSH(ctx, plan, address)
	if err != nil {
		return err
	}

	for _, item := range setupStages(ctx, plan, address) {
		err = plan.con.Run(item.label, item.running, item.action)
		if err != nil {
			return err
		}
	}

	needsInclude := includeSSHConfig(plan)
	printCreateDone(plan, time.Since(started), needsInclude)

	return nil
}

// requireTag refuses a box that joined without the tag. Untagged, it would
// be this user's device on the tailnet, with every rule that implies.
func requireTag(name, tag string, tags []string) error {
	if slices.Contains(tags, tag) {
		return nil
	}

	return abort.Hint(
		name+" joined the tailnet without "+tag,
		"The auth key did not carry the tag, so the box counts as one of your devices.",
		"Generate auth keys with the tag "+tag+", or let init set up an OAuth client that does.",
	)
}

// connectSSH is the first login. When the tailnet policy has no rule for it,
// the rule is shown and create waits for it to be added: the policy applies
// as soon as it is saved, and the box is fine otherwise.
func connectSSH(ctx context.Context, plan createPlan, address string) error {
	args := sshProbeArgs(config.AdminUser, address, plan.ssh.BoxKnownHosts)

	err := plan.con.Run("SSH", "connecting", func() (string, error) {
		return "reachable", waitForSSH(ctx, plan.name, address, args)
	})
	if !errors.Is(err, errPolicyDenied) {
		return err
	}

	plan.con.Note("The tailnet policy has no rule for " + config.AdminUser + "@" + plan.name +
		". Add this line to its ssh section and save:")
	plan.con.Command(tailscale.SSHRule(plan.cfg.Tag))
	plan.con.Blank()

	return plan.con.Run("SSH", fmt.Sprintf("waiting for the policy, up to %dm", policyWaitMinutes()),
		func() (string, error) {
			return "reachable", waitForPolicy(ctx, plan.name, args)
		})
}

func setupStages(ctx context.Context, plan createPlan, address string) []stage {
	cfg := plan.cfg
	knownHosts := plan.ssh.BoxKnownHosts

	return []stage{
		{"cloud-init", "finishing setup", func() (string, error) {
			return "done", waitForCloudInit(ctx, plan.name, address, knownHosts)
		}},
		{"Docker", "checking", func() (string, error) {
			return "rootless, running", assertRootlessDocker(ctx, config.AgentUser, plan.name, address, knownHosts)
		}},
		{"Git", "copying key", func() (string, error) {
			return cfg.GitUserName + " <" + cfg.GitUserEmail + ">", installGitAccess(ctx, cfg, address, knownHosts)
		}},
		{"SSH config", "adding Host " + plan.name, func() (string, error) {
			return "Host " + plan.name + " added", openssh.AppendHost(plan.ssh, plan.name, address, config.AgentUser)
		}},
	}
}

// includeSSHConfig never fails create: the box is ready either way. It
// reports whether the user still has to add the Include line by hand.
func includeSSHConfig(plan createPlan) bool {
	added, problem := openssh.EnsureInclude(plan.ssh)

	switch {
	case added:
		plan.con.Done("Include", "added to "+console.DisplayPath(plan.ssh.UserConfig))
	case problem != "":
		plan.con.Skip("Include", console.DisplayPath(plan.ssh.UserConfig)+" left unchanged: "+problem)
	}

	return problem != ""
}

func printCreateDone(plan createPlan, elapsed time.Duration, needsInclude bool) {
	plan.con.Blank()
	plan.con.Say(plan.name+" is ready", " in "+console.FormatElapsed(elapsed))
	plan.con.Blank()

	if needsInclude {
		plan.con.Note("ssh " + plan.name + " needs this line at the top of " + console.DisplayPath(plan.ssh.UserConfig) + ":")
		plan.con.Command(openssh.IncludeLine)
		plan.con.Blank()
	}

	plan.con.Commands([][2]string{
		{"ssh " + plan.name, "connect as " + config.AgentUser + ", projects in ~/projects"},
		{"ssh " + config.AdminUser + "@" + plan.name, "with sudo"},
		{abort.Prog() + " delete " + plan.name, "delete it when you are done"},
	})
}

func parseCreateArgs(args []string) (string, error) {
	name := ""

	var err error
	for len(args) > 0 && err == nil {
		name, args, err = consumeNameArg(cmdCreate, name, args)
	}

	if err != nil {
		return "", err
	}

	if name == "" {
		name = defaultServerName()
	}

	return name, nil
}

func defaultServerName() string {
	return "build-" + time.Now().Format("20060102-150405")
}

func validateName(name string) error {
	if regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`).MatchString(name) {
		return nil
	}

	return abort.Hint(
		"Invalid name: "+name,
		"",
		fmt.Sprintf("Use lowercase letters, digits, and hyphens, at most %d characters, "+
			"not starting or ending with a hyphen.", nameMaxLength),
	)
}

func rejectExistingName(ctx context.Context, name string, paths openssh.Paths) error {
	exists, err := tailscale.MachineExists(ctx, name)
	if err != nil {
		return err
	}

	if exists {
		return abort.Hint(
			"A Tailscale machine named "+name+" is still on the tailnet",
			"delete does not remove it. Tailscale drops it after its server goes offline.",
			"If the server still exists, delete it with:\n"+
				abort.Indent+abort.Prog()+" delete "+name+"\n"+
				"Then wait until the machine has left the tailnet, and try again.",
		)
	}

	hosts, err := openssh.FindHosts(paths, name)
	if err != nil {
		return err
	}

	if len(hosts) > 0 {
		return abort.Hint(
			console.DisplayPath(hosts[0].Place.Config)+" already has a Host "+name+" entry",
			"",
			"Remove it with:\n"+abort.Indent+abort.Prog()+" delete "+name,
		)
	}

	return nil
}

// resolveAuthKey finds the one-time key the box joins with. TS_AUTHKEY wins,
// so a script can hand one over. Otherwise the OAuth client mints one, and
// without a client the user is asked, with the page and the choices spelled
// out. Each key is spent by the box, so none of this is reusable.
func resolveAuthKey(ctx context.Context, con *console.Console, cfg config.Config) (string, error) {
	if key := os.Getenv("TS_AUTHKEY"); key != "" {
		con.Done(authKeyLabel, authFromEnv)

		return key, checkAuthKey(key)
	}

	client := tailscale.OAuthClient{ID: cfg.OAuthClientID, Secret: cfg.OAuthClientSecret}
	if client.Configured() {
		var key string

		err := con.Run(authKeyLabel, "minting for "+cfg.Tag, func() (string, error) {
			minted, mintErr := client.MintAuthKey(ctx, cfg.Tag)
			key = minted

			return "minted for " + cfg.Tag + ", one-time", mintErr
		})

		return key, err
	}

	con.Note("Generate a one-time auth key at:")
	con.Command(tailscale.KeysURL)
	con.Note("Reusable off · Ephemeral on · Pre-approved on if your tailnet approves devices · Tags: " + cfg.Tag)

	key := promptAuthKey()
	if key == "" {
		return "", abort.Hint("A Tailscale auth key is required", "",
			"Enter it at the prompt, set TS_AUTHKEY, or let init set up an OAuth client.")
	}

	con.Done(authKeyLabel, authFromPrompt)

	return key, checkAuthKey(key)
}

func checkAuthKey(key string) error {
	if strings.ContainsAny(key, "'\n") {
		return abort.New("Tailscale auth key contains a character that would break cloud-init user-data.")
	}

	return nil
}

// promptAuthKey erases its prompt once answered. The "Auth key" step line
// takes its place, so the checklist stays one line per step.
func promptAuthKey() string {
	paint := console.StderrPaint()
	fmt.Fprint(os.Stderr, console.StepIndent+paint.Accent.Render("?")+" Tailscale auth key "+
		paint.Muted.Render("(one-time, ephemeral)")+" ")

	if !term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprintln(os.Stderr)

		return ""
	}

	entered, readErr := term.ReadPassword(int(os.Stdin.Fd()))

	fmt.Fprintln(os.Stderr)

	if term.IsTerminal(int(os.Stderr.Fd())) {
		fmt.Fprint(os.Stderr, cursorUpClear)
	}

	if readErr != nil {
		return ""
	}

	return string(entered)
}

// sshBaseArgs records the box's host key in buildbox's own known_hosts, the
// file its Host entry points at, so the user's known_hosts is never touched.
// These connections go to the address, which the Host entry's ForwardAgent no
// does not match, so a Host * that forwards the agent is overridden here.
func sshBaseArgs(knownHosts string) []string {
	return []string{
		"-o", "BatchMode=yes",
		"-o", "ForwardAgent=no",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "UserKnownHostsFile=" + knownHosts,
		"-o", fmt.Sprintf("ConnectTimeout=%d", sshConnectTimeout),
		"-o", "ServerAliveInterval=30",
		"-o", "ServerAliveCountMax=40",
	}
}

// sshProbeArgs is the no-op login that shows whether the box answers and
// whether the policy admits this device.
func sshProbeArgs(user, address, knownHosts string) []string {
	args := append([]string{"-n"}, sshBaseArgs(knownHosts)...)

	return append(args, user+"@"+address, "true")
}

func waitForSSH(ctx context.Context, name, address string, args []string) error {
	err := sshUntilReady(ctx, args, sshWaitAttempts, sshWaitGap, true)
	if errors.Is(err, errSSHTimeout) {
		return abort.Newf("Timed out waiting for SSH on %s (%s).", name, address)
	}

	return err
}

func waitForPolicy(ctx context.Context, name string, args []string) error {
	err := sshUntilReady(ctx, args, policyWaitAttempts, policyWaitGap, false)
	if errors.Is(err, errSSHTimeout) {
		return abort.Hint(
			"The tailnet policy still refuses SSH to "+name,
			"",
			"Add the rule above, then connect with:\n"+abort.Indent+"ssh "+config.AdminUser+"@"+name,
		)
	}

	return err
}

// sshUntilReady runs the probe until it succeeds or the attempts run out.
// With stopOnPolicy, a policy refusal ends it at once, since trying again
// would not change the policy.
func sshUntilReady(ctx context.Context, args []string, attempts int, gap time.Duration, stopOnPolicy bool) error {
	for attempt := 1; attempt <= attempts; attempt++ {
		result, err := openssh.Run(ctx, "ssh", args...)
		if err == nil {
			return nil
		}

		if stopOnPolicy && policyRefused(result.Stderr) {
			return errPolicyDenied
		}

		if attempt == attempts {
			break
		}

		sleepErr := pause(ctx, gap)
		if sleepErr != nil {
			return sleepErr
		}
	}

	return errSSHTimeout
}

func policyRefused(stderr string) bool {
	return strings.Contains(stderr, policyBanner)
}

func policyWaitMinutes() int {
	return int(time.Duration(policyWaitAttempts) * policyWaitGap / time.Minute)
}

// waitForCloudInit shows cloud-init's own summary when it fails, since the
// box is reachable and the reason is usually in it.
func waitForCloudInit(ctx context.Context, name, address, knownHosts string) error {
	args := append([]string{"-n"}, sshBaseArgs(knownHosts)...)
	args = append(args, config.AdminUser+"@"+address, "sudo -n cloud-init status --wait")

	_, err := openssh.Run(ctx, "ssh", args...)
	if err == nil {
		return nil
	}

	longArgs := append([]string{"-n"}, sshBaseArgs(knownHosts)...)
	longArgs = append(longArgs, config.AdminUser+"@"+address, "sudo -n cloud-init status --long")
	report, _ := openssh.Run(ctx, "ssh", longArgs...)

	return abort.Hint(
		"cloud-init did not finish successfully on "+name,
		report.Stdout,
		"Read the full log with:\n"+abort.Indent+"ssh "+config.AdminUser+"@"+address+
			" sudo cat /var/log/cloud-init-output.log",
	)
}

func assertRootlessDocker(ctx context.Context, user, name, address, knownHosts string) error {
	args := append([]string{"-n"}, sshBaseArgs(knownHosts)...)
	args = append(args, user+"@"+address, "docker info --format '{{.SecurityOptions}}'")

	result, err := openssh.Run(ctx, "ssh", args...)
	if err != nil || !strings.Contains(result.Stdout, "name=rootless") {
		return abort.Newf("Rootless Docker is not working for %s on %s.", user, name)
	}

	return nil
}

func installGitAccess(ctx context.Context, cfg config.Config, address, knownHosts string) error {
	target := config.AgentUser + "@" + address
	remoteKey := "/home/" + config.AgentUser + "/.ssh/id_ed25519"
	scpArgs := append(sshBaseArgs(knownHosts), cfg.GitSSHKeyPath, target+":"+remoteKey)

	_, err := openssh.Run(ctx, "scp", scpArgs...)
	if err != nil {
		return abort.Newf("Could not copy the Git SSH key to %s.", target)
	}

	chmodArgs := append([]string{"-n"}, sshBaseArgs(knownHosts)...)

	chmodArgs = append(chmodArgs, target, "chmod 600 ~/.ssh/id_ed25519")

	_, err = openssh.Run(ctx, "ssh", chmodArgs...)
	if err != nil {
		return abort.Newf("Could not protect the Git SSH key on %s.", target)
	}

	gitConfig := `cat > ~/.ssh/config << EOF
Host github.com
  IdentityFile ~/.ssh/id_ed25519
  IdentitiesOnly yes
Host gitlab.com
  IdentityFile ~/.ssh/id_ed25519
  IdentitiesOnly yes
EOF
chmod 600 ~/.ssh/config`

	configArgs := append([]string{"-n"}, sshBaseArgs(knownHosts)...)

	configArgs = append(configArgs, target, gitConfig)

	_, err = openssh.Run(ctx, "ssh", configArgs...)
	if err != nil {
		return abort.Newf("Could not write the Git SSH config on %s.", target)
	}

	identity := "git config --global user.name " + bashSingleQuote(cfg.GitUserName) +
		" && git config --global user.email " + bashSingleQuote(cfg.GitUserEmail)

	identityArgs := append([]string{"-n"}, sshBaseArgs(knownHosts)...)
	identityArgs = append(identityArgs, target, identity)

	_, err = openssh.Run(ctx, "ssh", identityArgs...)
	if err != nil {
		return abort.Newf("Could not set the Git identity on %s.", target)
	}

	return nil
}

func interrupted() error {
	return abort.New("Interrupted")
}

// createFailure is the error create ends with. After an interrupt, the
// step's own error would describe a killed ssh as a problem with the box, so
// it is replaced. A server that already exists gets the command that removes
// it, whatever the error.
func createFailure(ctx context.Context, err error, created bool, name string) error {
	if err == nil {
		return nil
	}

	if ctx.Err() != nil {
		err = interrupted()
	}

	if created {
		err = abort.WithHint(err, name+" still exists and is billed by the hour. Delete it with:\n"+
			abort.Indent+abort.Prog()+" delete "+name)
	}

	return err
}

func pause(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return abort.Newf("Interrupted while waiting.\n%s", ctx.Err().Error())
	case <-timer.C:
		return nil
	}
}

func bashSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
