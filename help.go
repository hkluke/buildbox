package main

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"

	"github.com/hkluke/buildbox/internal/abort"
	"github.com/hkluke/buildbox/internal/config"
	"github.com/hkluke/buildbox/internal/console"
	"github.com/hkluke/buildbox/internal/hetzner"
	"github.com/hkluke/buildbox/internal/release"
)

const (
	cmdInit       = "init"
	cmdCreate     = "create"
	cmdList       = "list"
	cmdPrice      = "price"
	cmdDelete     = "delete"
	cmdForward    = "forward"
	cmdHelp       = "help"
	aliasList     = "ls"
	aliasDelete   = "rm"
	shortRevision = 7
)

// errHelpShown means help was written to stdout and the process should exit 0.
var errHelpShown = errors.New("help shown")

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return showRootHelp()
	}

	switch args[0] {
	case flagHelpShort, flagHelp:
		if len(args) > 1 {
			return abort.Usage(flagHelp+" does not take arguments", "")
		}

		return showRootHelp()
	case flagVersion:
		if len(args) > 1 {
			return abort.Usage(flagVersion+" does not take arguments", "")
		}

		fmt.Println(abort.Prog() + " " + versionText())

		return nil
	default:
		return dispatch(ctx, canonicalCommand(args[0]), args[1:])
	}
}

// canonicalCommand maps the short aliases people type by reflex from other
// tools to the command they stand for.
func canonicalCommand(name string) string {
	switch name {
	case aliasList:
		return cmdList
	case aliasDelete:
		return cmdDelete
	default:
		return name
	}
}

func dispatch(ctx context.Context, name string, args []string) error {
	switch name {
	case cmdInit:
		return runInit(ctx, args)
	case cmdDelete:
		return runDelete(ctx, args)
	case cmdList:
		return runList(ctx, args)
	case cmdPrice:
		return runPrice(ctx, args)
	case cmdCreate:
		return runCreate(ctx, args)
	case cmdForward:
		return runForward(ctx, args)
	case cmdHelp:
		return runHelp(args)
	default:
		return abort.Usage("Unknown command: "+name, "")
	}
}

func runHelp(args []string) error {
	if len(args) == 0 {
		return showRootHelp()
	}

	if args[0] == flagHelpShort || args[0] == flagHelp {
		if len(args) > 1 {
			return abort.Usage("help takes at most one command", cmdHelp)
		}

		return showCommandHelp(cmdHelp)
	}

	if len(args) > 1 {
		return abort.Usage("help takes at most one command", cmdHelp)
	}

	return showCommandHelp(canonicalCommand(args[0]))
}

func showRootHelp() error {
	fmt.Print(console.StyleHelp(rootHelp(), console.StdoutPaint()))

	return errHelpShown
}

func showCommandHelp(name string) error {
	text, ok := commandHelp(name)
	if !ok {
		return abort.Hint("Unknown help topic: "+name, "", "Run "+abort.Prog()+" help for a list of commands.")
	}

	fmt.Print(console.StyleHelp(text, console.StdoutPaint()))

	return errHelpShown
}

func commandHelp(name string) (string, bool) {
	switch name {
	case cmdInit:
		return initHelp(), true
	case cmdCreate:
		return createHelp(), true
	case cmdList:
		return listHelp(), true
	case cmdPrice:
		return priceHelp(), true
	case cmdDelete:
		return deleteHelp(), true
	case cmdForward:
		return forwardHelp(), true
	case cmdHelp:
		return helpCommandHelp(), true
	default:
		return "", false
	}
}

// versionText is the module version for go install, or the Git commit for a
// build from a checkout, which is what a bug report needs.
func versionText() string {
	version := buildVersion()
	if version != "dev" {
		return version
	}

	info, ok := debug.ReadBuildInfo()
	if !ok {
		return version
	}

	revision, modified := "", false

	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value == "true"
		}
	}

	if revision == "" {
		return version
	}

	if len(revision) > shortRevision {
		revision = revision[:shortRevision]
	}

	if modified {
		revision += ", modified"
	}

	return version + " (" + revision + ")"
}

func helpRequested(args []string) bool {
	for _, arg := range args {
		if arg == flagHelp || arg == flagHelpShort {
			return true
		}
	}

	return false
}

func rootHelp() string {
	return fmt.Sprintf(`buildbox spins up a disposable VPS for your coding agent, reachable only
over Tailscale. Servers run on Hetzner Cloud.

Usage:
  %[1]s <command> [flags]
  %[1]s help <command>

Commands:
  init      Write the config and create the firewall
  create    Create a build box
  list      List build boxes (alias: ls)
  price     Show the prices of build boxes by server type and location
  forward   Forward local ports to a box
  delete    Delete a build box and its local SSH entry (alias: rm)
  help      Show help for a command

Examples:
  %[1]s init               Write the config, once
  %[1]s price --memory 8   Compare boxes with 8 GB or more across locations
  %[1]s create dev         Create a box and add Host dev to your SSH config
  ssh dev                     Connect as the agent user
  %[1]s forward dev 8001   Reach the box's port 8001 at localhost:8001
  %[1]s delete dev         Delete the box when the task is done

Flags:
  -h, --help      Show help
      --version   Show the version

Environment:
  HCLOUD_TOKEN              Hetzner API token. Overrides the active hcloud context.
  TS_OAUTH_CLIENT_ID        Tailscale OAuth client that mints the auth key for
  TS_OAUTH_CLIENT_SECRET    each create. Override the credentials file.
  TS_AUTHKEY                One auth key for this create. Wins over the OAuth client.
  BUILDBOX_NO_UPDATE_CHECK  Skip init's check for a newer release.
  NO_COLOR                  Turn off colors.

Configuration is read from buildbox.toml next to this binary, or from
~/.config/buildbox/config.toml, and the OAuth client from the credentials
file beside it. See buildbox.toml.example.

Use "%[1]s help <command>" for more information about a command.
`, abort.Prog())
}

func initHelp() string {
	return fmt.Sprintf(`Write the buildbox config from a terminal.

Usage:
  %[1]s init

The first run opens on a welcome: the three sections it goes through, Box,
Agent, and Tailnet, each a few screens, and the tailnet and the Hetzner
catalog checked as it loads. Enter walks the sections in order, with every
header showing how far along you are, and ends on a review, where Write
saves the config. Esc goes back a screen, to the welcome from the first.

A later run opens on an overview of the same sections. The cursor starts
on Box, or on the first section that still needs something; after a
section it moves to the next one that does, or to Write. Esc leaves a
section for the overview, and the overview without writing. Quitting asks
first when there are changes Write has not saved.

%[6]s

Every build box gets the Hetzner firewall %[2]s. If it is
missing, init creates it when you choose Write, with one inbound rule for
UDP %[3]s. An existing firewall is never changed.

The files are the ones buildbox already reads. With no config yet, go run
writes ./buildbox.toml and an installed buildbox writes
~/.config/buildbox/config.toml. The OAuth client goes to
buildbox.credentials.toml or credentials.toml beside it, mode 0600, so the
config itself holds no secret. The client is saved as soon as it is created
or checked, because its secret is shown only once; the config is replaced
only when you choose Write, and before the firewall is created, so a failure
there keeps your answers.

While it loads, a released buildbox asks the Go module proxy whether a
newer release is out, and the overview shows the command that installs it.

init needs a terminal, Tailscale running, and a Hetzner token. It checks all
three first and lists what is missing, with how to get it. It does not
create a server.

Flags:
  -h, --help   Show help for init

Environment:
  %[5]s  Set to any value to skip the check for a newer release.
`, abort.Prog(), hetzner.FirewallName, hetzner.TailscaleUDPPort, config.DefaultTag, release.OptOut,
		fmt.Sprintf(initSectionsHelp, config.DefaultTag))
}

// initSectionsHelp describes init's three sections, in the order a first
// run meets them.
const initSectionsHelp = `Box is the location, the server type, and the image. A terminal with room
shows the locations on a world map, where left and right move west and
east. Server types are listed for the location you choose, with the hourly
price Hetzner bills and the monthly cap, gross.

Agent is the Git key, the Git identity, and the coding tools. The Git key
is a new ed25519 key, a key already on this machine, or a path you type. A
generated one is shown at the end and put on the clipboard, with the GitHub
and GitLab pages it goes to. There is no login key: logins over the tailnet
are decided by the policy, through Tailscale SSH. Coding tools are installed
for the agent user; OpenCode is the usual choice.

Tailnet adds the three lines your policy needs for boxes tagged %[1]s and
the OAuth client that mints a one-time auth key for each create. With an API
access token you generate once, init shows the changes first, has Tailscale
check that your login can SSH to boxes as agent and admin, writes them when
you confirm, then creates the client and checks it. The token is used for
that and never stored. Or you do it by hand: init shows the lines, and
you create the client yourself or paste a key for each box. A credentials
file whose client still works marks the section as done; opening it again
repairs the policy or replaces the client.`

func createHelp() string {
	return fmt.Sprintf(`Create a build box on Hetzner and wait until it is reachable over Tailscale.

Usage:
  %[1]s create [NAME]
  %[1]s create --name NAME

The server is labeled buildbox=true. The name is also the hostname, the
Tailscale machine name, and a Host entry with the agent user, so ssh NAME
works here. On your other devices, ssh agent@NAME works with no entry,
since the tailnet policy decides the login. The default name is
build-YYYYMMDD-HHMMSS.

Host entries and their host keys live in ~/.ssh/buildbox, which buildbox
owns. ~/.ssh/config only needs one line at its top:
  Include ~/.ssh/buildbox/config
The first create adds it. If ~/.ssh/config is a symlink or cannot be
written, create leaves it alone and prints the line to add yourself.

The Hetzner firewall must allow only inbound UDP 41641. The box joins the
tailnet with a one-time auth key carrying the tag from the config, which
buildbox mints when an OAuth client is configured and asks for otherwise.
Logins are decided by your tailnet policy: when it has no rule for the
box yet, create shows the rule and waits for you to add it. After
cloud-init finishes, buildbox checks rootless Docker, copies the Git SSH
key, and records the host key.

Flags:
      --name NAME   Server name, the same as NAME. Lowercase letters,
                    digits, and hyphens, at most 63 characters. It
                    cannot start or end with a hyphen.
  -h, --help        Show help for create

Environment:
  TS_AUTHKEY              One-time auth key for this create. It must carry
                          the tag. Wins over the OAuth client.
  TS_OAUTH_CLIENT_ID      OAuth client used to mint the key. Override the
  TS_OAUTH_CLIENT_SECRET  credentials file beside the config.
`, abort.Prog())
}

func listHelp() string {
	return fmt.Sprintf(`List build boxes labeled buildbox=true. The alias ls does the same.

Usage:
  %s list [--json]

One row per server, with its status, server type, location, and age.
TAILSCALE is the machine address, "absent" when that machine is gone,
or "this machine" when the name points at this computer. A box keeps
costing money until it is deleted, so the age shows forgotten ones.

--json prints an array for scripts and agents. Each object has name,
status, type, location, created (RFC 3339), tailnet ("present",
"absent", or "self"), and address (the Tailscale IPv4, or null).

The Tailscale app must be running on this machine. buildbox reads its
local status and does not run the tailscale command. Servers created
before the label existed are not shown. list does not read ~/.ssh/config.

Flags:
      --json   Print JSON instead of a table
  -h, --help   Show help for list
`, abort.Prog())
}

func priceHelp() string {
	return fmt.Sprintf(`Show the prices of build boxes, by server type and location.

Usage:
  %[1]s price [--cores N] [--memory GB] [--disk GB] [--arch ARCH]
                 [--cpu KIND] [--location NAME] [--cheapest] [--json]
                 [--provider NAME] [--per PERIOD]
  %[1]s price [--min-cores N] [--min-memory GB] [--min-disk GB] ...
  %[1]s price [--max-cores N] [--max-memory GB] [--max-disk GB] ...

One row per server type that meets every filter, sorted by the lowest
price in the row where the type can be created. The row shows the type's
cores, memory, disk, architecture, and CPU kind, then the price in each
location. A location where the type is sold out says "sold out"; one
where it is not offered says "-". With no filters, every type is listed.
The type and location in the config, when there is one, are marked.

--cheapest answers with one type instead: the cheapest that fits where
it can be created, every location sharing that price, and both figures.
Nothing fitting is an error, so a script can tell.

Hetzner bills by the hour and caps a month at the monthly price. The
table shows the cap unless --per hour asks for the rate; a line above
it says which. Prices are gross, with VAT, for the server type alone;
the box's primary IPv4 is billed on top.

--cores, --memory, and --disk are exact: --memory 8 lists the 8 GB types,
and --memory 8,16 or --memory 8 --memory 16 lists both sizes. A range as
8-16 takes every size between, and lists like a size: --memory 8-16,32.
The --min- and --max- forms bound a size from one side each; they mix
with each other, not with the exact flag. --cheapest --min-memory 8 is
the cheapest box with at least 8 GB. Memory and disk are gigabytes, bare
or as 8G or 8GB; a T or TB suffix means a thousand gigabytes, as Hetzner
counts disk, so --max-disk 1TB is 1000 GB. In a range, a unit on the right
applies to a bare left, so 1-2TB is one to two terabytes.

price needs the provider's token, no config, and creates nothing.
`, abort.Prog()) + priceFlagsHelp()
}

// priceFlagsHelp is the second half of price's help, split from the first
// only to keep each function within the length the linter allows.
func priceFlagsHelp() string {
	return `Flags:
      --provider NAME    The cloud provider. Only hetzner for now, the default.
      --per PERIOD       month, the cap (default), or hour, the rate billed.
      --cores N          Exactly N vCPUs, shared or dedicated alike. Several
                         as 2,4, a range as 2-4, or repeats; any of them fits.
      --memory GB        Exactly GB gigabytes of memory; several or a range
                         likewise.
      --disk GB          Exactly GB gigabytes of disk; several or a range
                         likewise.
      --min-cores N      At least N vCPUs.
      --min-memory GB    At least GB gigabytes of memory.
      --min-disk GB      At least GB gigabytes of disk.
      --max-cores N      At most N vCPUs.
      --max-memory GB    At most GB gigabytes of memory.
      --max-disk GB      At most GB gigabytes of disk.
      --arch ARCH        Only x86 or only arm. Without it, both.
      --cpu KIND         Only shared or only dedicated. Shared vCPUs cost
                         less and can slow under a neighbor's load.
      --location NAME    Only this location, such as fsn1 or ASH as the
                         table prints it. Several as nbg1,hel1 or by
                         repeating the flag. Without it, every location
                         is a column.
      --cheapest         Only the cheapest type that fits, and where. Both
                         figures are shown, so --per does not apply.
      --json             Print JSON instead of a table: one object per
                         type with provider, type, cores, memory, disk (GB),
                         storage ("local" or "network"), arch, cpu, and
                         prices, a map from location to hourly, monthly,
                         currency, and availability ("available",
                         "sold_out", or "not_offered"), null prices where
                         not offered. With --cheapest, one object: provider,
                         type, the specs, locations, hourly, monthly, currency.
  -h, --help             Show help for price

Environment:
  HCLOUD_TOKEN   Hetzner API token. Overrides the active hcloud context.
`
}

func deleteHelp() string {
	return fmt.Sprintf(`Delete a build box and the local SSH entry for that name. The alias rm
does the same.

Usage:
  %[1]s delete NAME [--force]
  %[1]s delete --name NAME [--force]

Only servers labeled buildbox=true are deleted, so a typo cannot remove
another server in the Hetzner project. --force deletes the server
without that label, for example one made before buildbox labeled them.

The Hetzner server is deleted first. The Host entry and its host key are
removed from ~/.ssh/buildbox only after that succeeds. A box created
before buildbox had that directory is removed from ~/.ssh/config and
~/.ssh/known_hosts instead. A missing piece is skipped, so delete can be
run again after a partial failure.

The Tailscale machine is ephemeral. delete does not remove it. Tailscale
drops the machine after the server goes offline.

Flags:
      --name NAME   Server name. Same character rules as create.
      --force       Delete the server even without the buildbox=true label
  -h, --help        Show help for delete
`, abort.Prog())
}

func forwardHelp() string {
	return fmt.Sprintf(`Forward ports on this machine to a build box over Tailscale SSH, until
Ctrl-C.

Usage:
  %[1]s forward NAME PORT...
  %[1]s forward --name NAME PORT...

Each PORT is LOCAL:REMOTE, or one number used on both sides. Connections
to localhost:LOCAL on this machine reach localhost:REMOTE on the box.
Several ports share one ssh session:
  %[1]s forward dev 8001              localhost:8001 → dev:8001
  %[1]s forward dev 8000:8001 5432    localhost:8000 → dev:8001, and 5432

This is ssh -L over Tailscale SSH, bound to localhost on both ends: the
port on this machine is not reachable from your network, and the forward
reaches nothing on the box but the box itself. A service on the box that
listens on all interfaces needs no forward; your tailnet already reaches
it at http://NAME:PORT. forward is for services bound to 127.0.0.1 on
the box, and for tools on this machine that must see localhost.

forward finds the box on the tailnet, so Tailscale must be running here,
and logs in as %[2]s, so the policy rule create needs applies. It reads no
config and does not call Hetzner. Each local port is checked to be free
before ssh starts. While the forward runs, what ssh prints is shown as it
comes, such as a refused connection when nothing listens on the box's
port. Ctrl-C ends it; the box is untouched.

Remote forwarding, from the box to this machine, is not offered.

Flags:
      --name NAME   Box name. Same character rules as create.
  -h, --help        Show help for forward
`, abort.Prog(), config.AgentUser)
}

func helpCommandHelp() string {
	return fmt.Sprintf(`Show help for buildbox or for one command.

Usage:
  %s help [command]

With no command, help lists the commands. With a command name, it shows
that command's usage, flags, and behavior.

The same text is printed by <command> --help and <command> -h.

Commands:
  init, create, list, price, forward, delete, help

Flags:
  -h, --help   Show help for help
`, abort.Prog())
}

func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return "dev"
	}

	return info.Main.Version
}
