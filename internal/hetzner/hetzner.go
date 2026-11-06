// Package hetzner is every Hetzner Cloud call buildbox makes, through
// hcloud-go: the token, the firewall, servers, and the catalog init shows.
package hetzner

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/pelletier/go-toml/v2"

	"github.com/hkluke/buildbox/internal/abort"
	"github.com/hkluke/buildbox/internal/config"
)

// The label marks the servers and the firewall buildbox created, so list and
// delete never touch anything else in the project.
const (
	LabelKey      = "buildbox"
	LabelValue    = "true"
	LabelSelector = LabelKey + "=" + LabelValue
)

// FirewallName is the Cloud Firewall buildbox owns and attaches to every box.
const FirewallName = "buildbox-tailscale-only"

// TailscaleUDPPort is the only inbound port the firewall allows.
const TailscaleUDPPort = "41641"

const (
	bitsPerByte   = 8
	apiTimeout    = 2 * time.Minute
	createTimeout = 15 * time.Minute
	// catalogTimeout bounds init's load, which someone watches on the
	// loading screen. Other calls keep apiTimeout.
	catalogTimeout = 30 * time.Second
)

// TokenHint says where a Hetzner token comes from and which access it needs.
// The lines indented by abort.Indent are links, shown unwrapped.
const TokenHint = "Create a Hetzner API token with Read & Write access, ideally in a project used only by buildbox:\n" +
	abort.Indent + "https://docs.hetzner.com/cloud/api/getting-started/generating-api-token/\n" +
	"Then store it with the hcloud CLI, or set HCLOUD_TOKEN:\n" +
	abort.Indent + "https://github.com/hetznercloud/cli/blob/main/docs/tutorials/setup-hcloud-cli.md"

type hcloudCLIConfig struct {
	ActiveContext string          `toml:"active_context"`
	Contexts      []hcloudContext `toml:"contexts"`
}

type hcloudContext struct {
	Name  string `toml:"name"`
	Token string `toml:"token"`
}

// NewClient finds the token and returns a client that names buildbox and
// version in its user agent.
func NewClient(version string) (*hcloud.Client, error) {
	token, err := hetznerToken()
	if err != nil {
		return nil, err
	}

	client := hcloud.NewClient(
		hcloud.WithToken(token),
		hcloud.WithApplication("buildbox", version),
	)

	return client, nil
}

// hetznerToken follows the hcloud CLI: HCLOUD_TOKEN wins, otherwise the
// active context in the CLI's cli.toml. hcloud-go does not read that file itself.
func hetznerToken() (string, error) {
	if token := os.Getenv("HCLOUD_TOKEN"); token != "" {
		return token, nil
	}

	return tokenFromCLIConfig()
}

func tokenFromCLIConfig() (string, error) {
	path, err := hcloudConfigPath()
	if err != nil {
		return "", err
	}

	cfg, err := readHCloudConfig(path)
	if err != nil {
		return "", err
	}

	return tokenFromContext(cfg)
}

func readHCloudConfig(path string) (hcloudCLIConfig, error) {
	payload, err := os.ReadFile(path) // #nosec G304 -- the path is the hcloud CLI config, or HCLOUD_CONFIG.
	if err != nil {
		if os.IsNotExist(err) {
			return hcloudCLIConfig{}, abort.Hint("No Hetzner token found", "", TokenHint)
		}

		return hcloudCLIConfig{}, abort.Newf("Could not read %s.\n%s", path, err.Error())
	}

	var cfg hcloudCLIConfig

	unmarshalErr := toml.Unmarshal(payload, &cfg)
	if unmarshalErr != nil {
		return hcloudCLIConfig{}, abort.Newf("Could not read %s.\n%s", path, unmarshalErr.Error())
	}

	return cfg, nil
}

func tokenFromContext(cfg hcloudCLIConfig) (string, error) {
	name := os.Getenv("HCLOUD_CONTEXT")
	if name == "" {
		name = cfg.ActiveContext
	}

	if name == "" {
		return "", abort.Hint("No Hetzner token found", "", TokenHint)
	}

	for _, item := range cfg.Contexts {
		if item.Name == name && item.Token != "" {
			return item.Token, nil
		}
	}

	return "", abort.Hint("hcloud context "+name+" has no token", "", TokenHint)
}

func hcloudConfigPath() (string, error) {
	if path := os.Getenv("HCLOUD_CONFIG"); path != "" {
		return path, nil
	}

	dir, err := hcloudConfigDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, "hcloud", "cli.toml"), nil
}

// hcloudConfigDir is the directory the hcloud CLI uses. On macOS that is
// ~/.config, not ~/Library/Application Support.
func hcloudConfigDir() (string, error) {
	if runtime.GOOS == "windows" {
		dir, dirErr := os.UserConfigDir()
		if dirErr != nil {
			return "", abort.Newf("Could not find the hcloud config directory.\n%s", dirErr.Error())
		}

		return dir, nil
	}

	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return dir, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", abort.Newf("Could not find the hcloud config directory.\n%s", err.Error())
	}

	return filepath.Join(home, ".config"), nil
}

func withAPITimeout(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}

	return context.WithTimeout(parent, apiTimeout)
}

// AssertFirewall fails unless the firewall called name exists and allows
// nothing inbound but Tailscale.
func AssertFirewall(ctx context.Context, client *hcloud.Client, name string) error {
	callCtx, cancel := withAPITimeout(ctx)
	defer cancel()

	firewall, _, err := client.Firewall.GetByName(callCtx, name)
	if err != nil {
		return abort.Newf("Could not read Hetzner firewall %s.\n%s", name, err.Error())
	}

	if firewall == nil {
		return abort.Hint(
			"Hetzner firewall "+name+" does not exist",
			"",
			"Create "+FirewallName+" with:\n"+abort.Indent+abort.Prog()+" init",
		)
	}

	bad, inbound := inboundFirewallProblems(firewall.Rules)
	if len(bad) == 0 && inbound >= 1 {
		return nil
	}

	if len(bad) == 0 {
		return abort.Hint(
			"Firewall "+name+" has no inbound UDP "+TailscaleUDPPort+" rule",
			"",
			"Add one in the Hetzner Console: inbound, UDP, port "+TailscaleUDPPort+", any source.",
		)
	}

	return abort.Hint(
		"Firewall "+name+" allows more than Tailscale",
		strings.Join(bad, "\n"),
		"Remove the extra rules in the Hetzner Console, or with hcloud firewall delete-rule.",
	)
}

// CreateFirewall is called by init only when the firewall is missing. An
// existing firewall may be attached to other servers, so it is never changed.
func CreateFirewall(ctx context.Context, client *hcloud.Client) error {
	callCtx, cancel := withAPITimeout(ctx)
	defer cancel()

	result, _, err := client.Firewall.Create(callCtx, tailscaleFirewallOpts())
	if hcloud.IsError(err, hcloud.ErrorCodeForbidden) {
		return abort.Hint("Could not create Hetzner firewall "+FirewallName, err.Error(),
			"The Hetzner token can only read. Give buildbox a token with Read & Write access to this project.")
	}

	if err != nil {
		return abort.Newf("Could not create Hetzner firewall %s.\n%s", FirewallName, err.Error())
	}

	if len(result.Actions) > 0 {
		waitErr := client.Action.WaitFor(callCtx, result.Actions...)
		if waitErr != nil {
			return abort.Newf("Could not create Hetzner firewall %s.\n%s", FirewallName, waitErr.Error())
		}
	}

	return nil
}

// tailscaleFirewallOpts has no outbound rules, which Hetzner treats as allow
// all, so the box can still reach package mirrors, Git hosts, and Tailscale.
func tailscaleFirewallOpts() hcloud.FirewallCreateOpts {
	return hcloud.FirewallCreateOpts{
		Name:   FirewallName,
		Labels: map[string]string{LabelKey: LabelValue},
		Rules: []hcloud.FirewallRule{{
			Direction: hcloud.FirewallRuleDirectionIn,
			Protocol:  hcloud.FirewallRuleProtocolUDP,
			Port:      new(TailscaleUDPPort),
			SourceIPs: []net.IPNet{
				{IP: net.IPv4zero.To4(), Mask: net.CIDRMask(0, net.IPv4len*bitsPerByte)},
				{IP: net.IPv6zero, Mask: net.CIDRMask(0, net.IPv6len*bitsPerByte)},
			},
			Description: new("Tailscale"),
		}},
	}
}

func inboundFirewallProblems(rules []hcloud.FirewallRule) ([]string, int) {
	inbound := 0

	var bad []string

	for _, rule := range rules {
		if rule.Direction != hcloud.FirewallRuleDirectionIn {
			continue
		}

		inbound++

		if tailscaleRule(rule) {
			continue
		}

		bad = append(bad, fmt.Sprintf("inbound %s port %s", rule.Protocol, shownPort(rule.Port)))
	}

	return bad, inbound
}

func tailscaleRule(rule hcloud.FirewallRule) bool {
	return rule.Protocol == hcloud.FirewallRuleProtocolUDP && shownPort(rule.Port) == TailscaleUDPPort
}

func shownPort(port *string) string {
	if port == nil || *port == "" {
		return "(none)"
	}

	return *port
}

// CreateServer creates the labeled server with the config's firewall and
// waits for it to start. The bool reports whether the server exists, so a
// failure after the create still tells the caller there is one to delete.
func CreateServer(
	ctx context.Context,
	client *hcloud.Client,
	cfg config.Config,
	name string,
	userData string,
) (bool, error) {
	callCtx, cancel := context.WithTimeout(ctx, createTimeout)
	defer cancel()

	firewall, _, err := client.Firewall.GetByName(callCtx, cfg.FirewallName)
	if err != nil || firewall == nil {
		detail := "not found"
		if err != nil {
			detail = err.Error()
		}

		return false, abort.Newf("Could not read Hetzner firewall %s.\n%s", cfg.FirewallName, detail)
	}

	// Once the create request is sent it is allowed to finish, even after
	// Ctrl-C, so a server it makes is reported and can be deleted. Only the
	// wait for the server's actions can be interrupted.
	createCtx, cancelCreate := context.WithTimeout(context.WithoutCancel(ctx), createTimeout)
	defer cancelCreate()

	result, _, err := client.Server.Create(createCtx, hcloud.ServerCreateOpts{
		Name:       name,
		ServerType: &hcloud.ServerType{Name: cfg.ServerType},
		Image:      &hcloud.Image{Name: cfg.ServerImage},
		Location:   &hcloud.Location{Name: cfg.ServerLoc},
		UserData:   userData,
		Labels: map[string]string{
			LabelKey: LabelValue,
		},
		StartAfterCreate: new(true),
		Firewalls: []*hcloud.ServerCreateFirewall{{
			Firewall: hcloud.Firewall{ID: firewall.ID},
		}},
	})
	if err != nil {
		return false, abort.Newf("Could not create Hetzner server %s.\n%s", name, err.Error())
	}

	actions := make([]*hcloud.Action, 0, 1+len(result.NextActions))
	if result.Action != nil {
		actions = append(actions, result.Action)
	}

	actions = append(actions, result.NextActions...)
	if len(actions) > 0 {
		waitErr := client.Action.WaitFor(callCtx, actions...)
		if waitErr != nil {
			return true, abort.Newf("Could not create Hetzner server %s.\n%s", name, waitErr.Error())
		}
	}

	return true, nil
}

// ListServers returns the servers buildbox labeled.
func ListServers(ctx context.Context, client *hcloud.Client) ([]*hcloud.Server, error) {
	callCtx, cancel := withAPITimeout(ctx)
	defer cancel()

	servers, err := client.Server.AllWithOpts(callCtx, hcloud.ServerListOpts{
		LabelSelector: LabelSelector,
	})
	if err != nil {
		return nil, abort.Newf("Could not list Hetzner servers.\n%s", err.Error())
	}

	return servers, nil
}

// FindServer returns the server called name, or nil when there is none.
func FindServer(ctx context.Context, client *hcloud.Client, name string) (*hcloud.Server, error) {
	callCtx, cancel := withAPITimeout(ctx)
	defer cancel()

	server, _, err := client.Server.GetByName(callCtx, name)
	if err != nil {
		return nil, abort.Newf("Could not read Hetzner server %s.\n%s", name, err.Error())
	}

	return server, nil
}

// DeleteServer deletes server and waits until Hetzner confirms it.
func DeleteServer(ctx context.Context, client *hcloud.Client, server *hcloud.Server) error {
	callCtx, cancel := withAPITimeout(ctx)
	defer cancel()

	result, _, err := client.Server.DeleteWithResult(callCtx, server)
	if err != nil {
		return abort.Newf("Hetzner server %s was not deleted.\n%s", server.Name, err.Error())
	}

	if result.Action != nil {
		waitErr := client.Action.WaitFor(callCtx, result.Action)
		if waitErr != nil {
			return abort.Newf("Hetzner server %s was not deleted.\n%s", server.Name, waitErr.Error())
		}
	}

	return nil
}
