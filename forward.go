package main

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hkluke/buildbox/internal/abort"
	"github.com/hkluke/buildbox/internal/config"
	"github.com/hkluke/buildbox/internal/console"
	"github.com/hkluke/buildbox/internal/openssh"
	"github.com/hkluke/buildbox/internal/tailscale"
)

const (
	maxPort   = 65535
	localhost = "localhost"
	// forwardAliveCountMax, with the 30 second ServerAliveInterval, ends a
	// forward to a box that stopped answering within about 90 seconds.
	// create's value waits twenty minutes, for cloud-init.
	forwardAliveCountMax = 3
)

// portPair is one forward: the port on this machine and the port on the box.
type portPair struct {
	local  int
	remote int
}

func (pair portPair) describe(name string) string {
	return localhost + ":" + strconv.Itoa(pair.local) + " → " + name + ":" + strconv.Itoa(pair.remote)
}

func runForward(ctx context.Context, args []string) error {
	name, pairs, err := parseForwardArgs(args)
	if err != nil {
		return err
	}

	paths, err := prepareForward(ctx, name, pairs)
	if err != nil {
		return err
	}

	address, err := resolveBox(ctx, name)
	if err != nil {
		return err
	}

	con := console.New()
	con.Header(cmdForward, name)
	con.Done("Box", address+" on the tailnet")

	err = probeSSH(ctx, con, name, address, paths.BoxKnownHosts)
	if err != nil {
		return err
	}

	for _, pair := range pairs {
		con.Done("Forward", pair.describe(name))
	}

	con.Blank()
	con.Note("Press Ctrl-C to stop.")

	return holdForward(ctx, con, name, forwardSSHArgs(address, paths.BoxKnownHosts, pairs))
}

// prepareForward is every check that needs no network: the name, ssh, the
// local ports, and the directory ssh records the host key in, which ssh
// does not create itself.
func prepareForward(ctx context.Context, name string, pairs []portPair) (openssh.Paths, error) {
	err := validateName(name)
	if err != nil {
		return openssh.Paths{}, err
	}

	err = openssh.Require("ssh")
	if err != nil {
		return openssh.Paths{}, err
	}

	err = requirePortsFree(ctx, name, pairs)
	if err != nil {
		return openssh.Paths{}, err
	}

	paths, err := openssh.NewPaths()
	if err != nil {
		return openssh.Paths{}, err
	}

	err = os.MkdirAll(filepath.Dir(paths.BoxKnownHosts), openssh.DirPerm)
	if err != nil {
		return openssh.Paths{}, abort.Newf("Could not create %s.\n%s", filepath.Dir(paths.BoxKnownHosts), err.Error())
	}

	return paths, nil
}

// resolveBox finds the box on the tailnet, the way list does, so a box made
// on another device is found as well, and an absent one is reported before
// ssh waits on it.
func resolveBox(ctx context.Context, name string) (string, error) {
	_, err := tailscale.RequireUp(ctx)
	if err != nil {
		return "", err
	}

	state, err := tailscale.PeerState(ctx, name)
	if err != nil {
		return "", err
	}

	switch state {
	case tailscale.PeerAbsent:
		return "", abort.Hint(
			"No machine named "+name+" on the tailnet",
			"",
			"See your boxes with:\n"+abort.Indent+abort.Prog()+" list",
		)
	case tailscale.PeerSelf:
		return "", abort.New(name + " is this machine.")
	default:
		return state, nil
	}
}

// probeSSH is one login as the agent user, so the forward starts only on a
// box that answers and under a policy that admits this device. Its output
// carries Tailscale's reason when the policy refuses.
func probeSSH(ctx context.Context, con *console.Console, name, address, knownHosts string) error {
	args := sshProbeArgs(config.AgentUser, address, knownHosts)

	return con.Run("SSH", "connecting", func() (string, error) {
		result, err := openssh.Run(ctx, "ssh", args...)
		if err == nil {
			return "reachable as " + config.AgentUser, nil
		}

		if ctx.Err() != nil {
			return "", interrupted()
		}

		return "", abort.Newf("Could not reach %s over SSH.\n%s", name, strings.TrimSpace(result.Stderr))
	})
}

// holdForward keeps ssh up until Ctrl-C. ssh -N prints nothing while the
// forwards work, so every line it does print is shown as it comes. Ctrl-C
// is how the command ends, not a failure, so it exits cleanly.
func holdForward(ctx context.Context, con *console.Console, name string, args []string) error {
	started := time.Now()

	ended := openssh.Stream(ctx, func(line string) { con.Note(describeSSHLine(name, line)) }, "ssh", args...)

	select {
	case <-ctx.Done():
		con.Blank()
		con.Say("Stopped", " after "+console.FormatElapsed(time.Since(started)))

		return nil
	default:
		return abort.Hint(
			"The forward to "+name+" ended",
			sshExitDetail(ended),
			"Check the box with:\n"+abort.Indent+abort.Prog()+" list",
		)
	}
}

func sshExitDetail(err error) string {
	var exit *exec.ExitError

	switch {
	case err == nil:
		return "ssh closed the connection."
	case errors.As(err, &exit):
		return "ssh ended with " + exit.Error() + "."
	default:
		return err.Error()
	}
}

// forwardSSHArgs is the ssh command that holds the forwards. Its own two
// options come before the shared ones because ssh keeps the first value it
// sees for an option: ExitOnForwardFailure ends ssh when a port cannot be
// bound, instead of keeping a session without the forward, and the lower
// ServerAliveCountMax ends a forward to a box that stopped answering.
func forwardSSHArgs(address, knownHosts string, pairs []portPair) []string {
	args := []string{
		"-N", "-n",
		"-o", "ExitOnForwardFailure=yes",
		"-o", "ServerAliveCountMax=" + strconv.Itoa(forwardAliveCountMax),
	}
	args = append(args, sshBaseArgs(knownHosts)...)

	// localhost on both ends: the port on this machine stays off the LAN,
	// and the forward reaches nothing on the box but the box itself.
	for _, pair := range pairs {
		args = append(args, "-L", localhost+":"+strconv.Itoa(pair.local)+":"+localhost+":"+strconv.Itoa(pair.remote))
	}

	return append(args, config.AgentUser+"@"+address)
}

// requirePortsFree binds each local port for a moment, so a port already in
// use is reported before ssh starts and every Forward line names a forward
// that exists.
func requirePortsFree(ctx context.Context, name string, pairs []portPair) error {
	var listen net.ListenConfig

	for _, pair := range pairs {
		listener, err := listen.Listen(ctx, "tcp", net.JoinHostPort(localhost, strconv.Itoa(pair.local)))
		if err != nil {
			return abort.Hint(
				"Port "+strconv.Itoa(pair.local)+" cannot be used on this machine",
				err.Error(),
				"Pick another local port:\n"+abort.Indent+abort.Prog()+" forward "+name+" LOCAL:"+strconv.Itoa(pair.remote),
			)
		}

		_ = listener.Close()
	}

	return nil
}

// describeSSHLine rewords the ssh messages a forward runs into, which speak
// of channels rather than of what went wrong. Other lines pass as they are.
func describeSSHLine(name, line string) string {
	lower := strings.ToLower(line)

	switch {
	case strings.Contains(lower, "open failed") && strings.Contains(lower, "connection refused"):
		return name + " refused a connection: nothing is listening on the remote port"
	case strings.Contains(lower, "administratively prohibited"):
		return "the tailnet policy does not allow port forwarding on " + name
	default:
		return line
	}
}

func parseForwardArgs(args []string) (string, []portPair, error) {
	name := ""

	var pairs []portPair

	for len(args) > 0 {
		arg := args[0]
		if arg == flagName || strings.HasPrefix(arg, flagNameEq) {
			var err error

			name, args, err = takeNameFlag(cmdForward, name, args)
			if err != nil {
				return "", nil, err
			}

			continue
		}

		args = args[1:]

		if strings.HasPrefix(arg, "-") {
			return "", nil, forwardFlagError(arg)
		}

		if name == "" {
			name = arg

			continue
		}

		pair, err := parsePortPair(arg)
		if err != nil {
			return "", nil, err
		}

		pairs = append(pairs, pair)
	}

	err := checkForwardArgs(name, pairs)
	if err != nil {
		return "", nil, err
	}

	return name, pairs, nil
}

// forwardFlagError is any flag but --name: help when asked for, a usage
// error otherwise.
func forwardFlagError(arg string) error {
	if arg == flagHelp || arg == flagHelpShort {
		return showCommandHelp(cmdForward)
	}

	return abort.Usage("Unknown argument: "+arg, cmdForward)
}

// checkForwardArgs is what parsing cannot tell one argument at a time: both
// parts are there, and no local port is asked for twice, which ssh would
// only refuse after connecting.
func checkForwardArgs(name string, pairs []portPair) error {
	if name == "" {
		return abort.Usage("forward needs the box name and a port", cmdForward)
	}

	if len(pairs) == 0 {
		return abort.Usage("forward needs at least one port", cmdForward)
	}

	seen := make(map[int]bool, len(pairs))
	for _, pair := range pairs {
		if seen[pair.local] {
			return abort.Usage("Local port "+strconv.Itoa(pair.local)+" is given twice", cmdForward)
		}

		seen[pair.local] = true
	}

	return nil
}

// parsePortPair reads LOCAL:REMOTE, or one port used on both sides.
func parsePortPair(arg string) (portPair, error) {
	local, remote, found := strings.Cut(arg, ":")
	if !found {
		remote = local
	}

	localPort, okLocal := parsePort(local)
	remotePort, okRemote := parsePort(remote)

	if !okLocal || !okRemote {
		return portPair{}, abort.Hint(
			"Not a port: "+arg,
			"",
			"Give one port, or LOCAL:REMOTE, each from 1 to "+strconv.Itoa(maxPort)+".",
		)
	}

	return portPair{local: localPort, remote: remotePort}, nil
}

func parsePort(text string) (int, bool) {
	port, err := strconv.Atoi(text)
	if err != nil || port < 1 || port > maxPort {
		return 0, false
	}

	return port, true
}
