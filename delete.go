package main

import (
	"context"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"

	"github.com/hkluke/buildbox/internal/abort"
	"github.com/hkluke/buildbox/internal/console"
	"github.com/hkluke/buildbox/internal/hetzner"
	"github.com/hkluke/buildbox/internal/openssh"
)

type deleteTarget struct {
	name   string
	client *hcloud.Client
	server *hcloud.Server
	ssh    openssh.Paths
	hosts  []openssh.Host
}

func runDelete(ctx context.Context, args []string) error {
	target, err := inspectDelete(ctx, args)
	if err != nil {
		return err
	}

	con := console.New()
	con.Header(cmdDelete, target.name)

	if target.server == nil && len(target.hosts) == 0 {
		con.Note("Nothing named " + target.name + " to delete: no server and no Host entry.")

		return nil
	}

	err = deleteHetzner(ctx, con, target)
	if err != nil {
		return err
	}

	err = removeLocalSSH(ctx, con, target)
	if err != nil {
		return err
	}

	con.Blank()
	con.Say(target.name+" is deleted", ". Tailscale removes its machine once it is offline.")

	return nil
}

func inspectDelete(ctx context.Context, args []string) (deleteTarget, error) {
	name, force, err := parseDeleteArgs(args)
	if err != nil {
		return deleteTarget{}, err
	}

	err = validateName(name)
	if err != nil {
		return deleteTarget{}, err
	}

	err = openssh.Require("ssh-keygen")
	if err != nil {
		return deleteTarget{}, err
	}

	client, err := newHetznerClient()
	if err != nil {
		return deleteTarget{}, err
	}

	server, err := hetzner.FindServer(ctx, client, name)
	if err != nil {
		return deleteTarget{}, err
	}

	err = requireLabel(server, force)
	if err != nil {
		return deleteTarget{}, err
	}

	paths, err := openssh.NewPaths()
	if err != nil {
		return deleteTarget{}, err
	}

	hosts, err := openssh.FindHosts(paths, name)
	if err != nil {
		return deleteTarget{}, err
	}

	return deleteTarget{name: name, client: client, server: server, ssh: paths, hosts: hosts}, nil
}

// requireLabel keeps delete to servers buildbox created, so a typo in the
// name cannot remove another server in the Hetzner project.
func requireLabel(server *hcloud.Server, force bool) error {
	if server == nil || force || server.Labels[hetzner.LabelKey] == hetzner.LabelValue {
		return nil
	}

	return abort.Hint(
		"Hetzner server "+server.Name+" was not created by buildbox",
		"It has no "+hetzner.LabelSelector+" label, so it may be something else you run.",
		"If you are sure, delete it anyway with:\n"+abort.Indent+abort.Prog()+" delete "+server.Name+" "+flagForce,
	)
}

// deleteHetzner runs first. If it fails, nothing local has changed, so the
// same command can simply be run again.
func deleteHetzner(ctx context.Context, con *console.Console, target deleteTarget) error {
	if target.server == nil {
		con.Skip("Server", "none named "+target.name)

		return nil
	}

	err := con.Run("Server", "deleting", func() (string, error) {
		return "deleted", hetzner.DeleteServer(ctx, target.client, target.server)
	})
	if err != nil {
		return abort.WithHint(err,
			"Nothing local was changed. Try again with:\n"+abort.Indent+abort.Prog()+" delete "+target.name)
	}

	return nil
}

// removeLocalSSH removes the Host block from every file that has one, and
// the host key from the known_hosts file that block used. A box created
// before buildbox had its own file is cleaned from the user's files.
func removeLocalSSH(ctx context.Context, con *console.Console, target deleteTarget) error {
	if len(target.hosts) == 0 {
		con.Skip("SSH config", "no Host "+target.name)

		return nil
	}

	for _, host := range target.hosts {
		err := openssh.RemoveHost(host.Place.Config, target.name)
		if err != nil {
			return err
		}

		where := ""
		if target.ssh.IsLegacy(host.Place) {
			where = " from " + console.DisplayPath(host.Place.Config)
		}

		con.Done("SSH config", "Host "+target.name+" removed"+where)

		err = removeHostKey(ctx, con, host)
		if err != nil {
			return err
		}
	}

	return nil
}

func removeHostKey(ctx context.Context, con *console.Console, host openssh.Host) error {
	if host.Address == "" {
		return nil
	}

	removed, err := openssh.RemoveKnownHost(ctx, host.Address, host.Place.KnownHosts)
	if err != nil {
		return err
	}

	switch {
	case removed:
		con.Done("known_hosts", host.Address+" removed")
	case !openssh.KnownHostSafe(host.Address):
		con.Skip("known_hosts", "left as is: unexpected characters in "+host.Address)
	default:
		con.Skip("known_hosts", "no entry for "+host.Address)
	}

	return nil
}

func parseDeleteArgs(args []string) (string, bool, error) {
	name, force := "", false

	var err error
	for len(args) > 0 && err == nil {
		if args[0] == flagForce {
			force = true
			args = args[1:]

			continue
		}

		name, args, err = consumeNameArg(cmdDelete, name, args)
	}

	if err != nil {
		return "", false, err
	}

	if name == "" {
		return "", false, abort.Usage("delete needs the server name", cmdDelete)
	}

	return name, force, nil
}
