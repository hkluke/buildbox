// Package tailscale checks the local Tailscale app through its API. It never
// runs the tailscale command; the new server runs tailscale up itself.
package tailscale

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"tailscale.com/client/local"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg/nodecap"

	"github.com/hkluke/buildbox/internal/abort"
)

// PeerState returns one of these, or the machine's IPv4 address.
const (
	PeerSelf   = "self"
	PeerAbsent = "absent"
)

const (
	tailscaleWaitAttempts = 90
	tailscaleWaitGap      = 10 * time.Second
	tailscaleRunning      = "Running"
)

// errTailscaleRetry means the local Tailscale app is not ready yet.
// create and list turn that into a final error. The wait after create retries it.
var errTailscaleRetry = errors.New("tailscale not ready")

func tailscaleStatus(ctx context.Context) (*ipnstate.Status, error) {
	// The zero Client uses the platform default: the Unix socket, or on macOS
	// the menu-bar app. UseSocketOnly stays false so that macOS fallback works.
	// A program that imports tailscale.com never shares the daemon's version
	// string, so a mismatch is not a failure. Status reports a real
	// incompatibility itself.
	status, err := (&local.Client{}).Status(ctx)
	if err != nil {
		if local.IsAccessDeniedError(err) {
			return nil, abort.Newf("Tailscale on this machine refused the status request.\n%s", err.Error())
		}

		return nil, retryTailscale(err.Error())
	}

	if status == nil || status.BackendState != tailscaleRunning {
		state := "unknown"
		if status != nil && status.BackendState != "" {
			state = status.BackendState
		}

		return nil, retryTailscale("Tailscale state is " + state + ".")
	}

	return status, nil
}

type tailscaleRetryError struct {
	message string
}

func (err *tailscaleRetryError) Error() string {
	return err.message
}

func (err *tailscaleRetryError) Unwrap() error {
	return errTailscaleRetry
}

func retryTailscale(detail string) error {
	message := "Tailscale is not up on this machine, so its machines cannot be checked."
	if detail != "" {
		message += "\n" + detail
	}

	return &tailscaleRetryError{message: message}
}

// RequireUp fails unless Tailscale runs here, and returns this machine's
// Tailscale IPv4 address.
func RequireUp(ctx context.Context) (string, error) {
	status, err := tailscaleStatus(ctx)
	if err != nil {
		return "", finishTailscaleError(err)
	}

	address, ok := selfIPv4(status)
	if !ok {
		return "", abort.New("Could not read this machine's Tailscale IPv4 address.")
	}

	return address, nil
}

// PeerState finds the machine called name on the tailnet. It is PeerAbsent,
// PeerSelf when the name points at this machine, or the IPv4 address.
func PeerState(ctx context.Context, name string) (string, error) {
	status, err := tailscaleStatus(ctx)
	if err != nil {
		return "", err
	}

	return peerStateIn(status, name)
}

// peerStateIn is PeerState for a status already read, kept apart so the
// decision can be tested without a running Tailscale.
func peerStateIn(status *ipnstate.Status, name string) (string, error) {
	self, _ := selfIPv4(status)

	matches := matchingPeers(status, name)
	if len(matches) > 1 {
		return "", abort.Newf("Tailscale has more than one machine named %s.", name)
	}

	if len(matches) == 0 {
		return PeerAbsent, nil
	}

	peer := matches[0]
	if peer == status.Self || (peerIPv4(peer) == self && self != "") {
		return PeerSelf, nil
	}

	address := peerIPv4(peer)
	if address == "" {
		return "", abort.Newf("Could not read an IPv4 address for Tailscale machine %s.", name)
	}

	return address, nil
}

// MachineExists reports whether the tailnet has a machine called name.
func MachineExists(ctx context.Context, name string) (bool, error) {
	state, err := PeerState(ctx, name)
	if err != nil {
		return false, finishTailscaleError(err)
	}

	return state != PeerAbsent, nil
}

// Joined is a box that has appeared on the tailnet: its IPv4 address and
// the tags the auth key gave it.
type Joined struct {
	Address string
	Tags    []string
}

// Wait returns the box once it is on the tailnet. The box upgrades packages
// and installs Docker before it runs tailscale up, so this is the longest
// wait of create.
func Wait(ctx context.Context, name string) (Joined, error) {
	for attempt := 1; attempt <= tailscaleWaitAttempts; attempt++ {
		status, err := tailscaleStatus(ctx)
		if err != nil {
			return Joined{}, finishTailscaleError(err)
		}

		state, err := peerStateIn(status, name)
		if err != nil {
			return Joined{}, finishTailscaleError(err)
		}

		if state == PeerSelf {
			return Joined{}, abort.Newf("%s points at this Tailscale machine.", name)
		}

		if state != PeerAbsent {
			return Joined{Address: state, Tags: peerTagsIn(status, name)}, nil
		}

		if attempt == tailscaleWaitAttempts {
			break
		}

		sleepErr := sleep(ctx, tailscaleWaitGap)
		if sleepErr != nil {
			return Joined{}, sleepErr
		}
	}

	return Joined{}, abort.Hint(
		fmt.Sprintf("%s did not join Tailscale within %d minutes", name, WaitMinutes()),
		"",
		"The auth key is the usual cause: already used, expired, not pre-approved on a\n"+
			"tailnet that requires device approval, or without the tag the box advertises.\n"+
			"Retry with a new key.",
	)
}

// peerTagsIn is the tags of the one machine called name, or nothing.
func peerTagsIn(status *ipnstate.Status, name string) []string {
	matches := matchingPeers(status, name)
	if len(matches) != 1 || matches[0].Tags == nil {
		return nil
	}

	return matches[0].Tags.AsSlice()
}

// Tailnet is what init needs to know about this machine's tailnet.
type Tailnet struct {
	Name     string
	Login    string
	Admin    bool
	MagicDNS bool
}

// Describe reads the tailnet this machine is on and who this machine is.
func Describe(ctx context.Context) (Tailnet, error) {
	status, err := tailscaleStatus(ctx)
	if err != nil {
		return Tailnet{}, finishTailscaleError(err)
	}

	return describeIn(status), nil
}

func describeIn(status *ipnstate.Status) Tailnet {
	info := Tailnet{}
	if status.CurrentTailnet != nil {
		info.Name = status.CurrentTailnet.Name
		info.MagicDNS = status.CurrentTailnet.MagicDNSEnabled
	}

	if status.Self == nil {
		return info
	}

	info.Admin = status.Self.CapMap.Contains(nodecap.Admin)

	if profile, ok := status.User[status.Self.UserID]; ok {
		info.Login = profile.LoginName
	}

	return info
}

// WaitMinutes is how long Wait gives the box to join.
func WaitMinutes() int {
	limit := time.Duration(tailscaleWaitAttempts) * tailscaleWaitGap

	return int(limit / time.Minute)
}

func finishTailscaleError(err error) error {
	if err == nil || errors.Is(err, abort.ErrAbort) {
		return err
	}

	headline, detail, _ := strings.Cut(err.Error(), "\n")

	return abort.Hint(strings.TrimSuffix(headline, "."), detail, "Start Tailscale on this machine and log in.")
}

func matchingPeers(status *ipnstate.Status, name string) []*ipnstate.PeerStatus {
	var found []*ipnstate.PeerStatus
	if peerMatches(status.Self, name) {
		found = append(found, status.Self)
	}

	for _, peer := range status.Peer {
		if peer == nil || peer == status.Self {
			continue
		}

		if peerMatches(peer, name) {
			found = append(found, peer)
		}
	}

	return found
}

func peerMatches(peer *ipnstate.PeerStatus, name string) bool {
	if peer == nil || name == "" {
		return false
	}

	if strings.EqualFold(peer.HostName, name) {
		return true
	}

	dnsName := strings.TrimSuffix(peer.DNSName, ".")
	if strings.EqualFold(dnsName, name) {
		return true
	}

	first, _, _ := strings.Cut(dnsName, ".")

	return first != "" && strings.EqualFold(first, name)
}

func selfIPv4(status *ipnstate.Status) (string, bool) {
	if status == nil {
		return "", false
	}

	if address := peerIPv4(status.Self); address != "" {
		return address, true
	}

	return firstIPv4(status.TailscaleIPs)
}

func peerIPv4(peer *ipnstate.PeerStatus) string {
	if peer == nil {
		return ""
	}

	address, _ := firstIPv4(peer.TailscaleIPs)

	return address
}

func firstIPv4(addrs []netip.Addr) (string, bool) {
	for _, addr := range addrs {
		if addr.Is4() {
			return addr.String(), true
		}
	}

	return "", false
}

func sleep(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return abort.Newf("Interrupted while waiting.\n%s", ctx.Err().Error())
	case <-timer.C:
		return nil
	}
}
