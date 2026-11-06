package tailscale

import (
	"errors"
	"net/netip"
	"strings"
	"testing"

	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
	"tailscale.com/tailcfg/nodecap"
	"tailscale.com/types/key"
	"tailscale.com/types/views"

	"github.com/hkluke/buildbox/internal/abort"
)

const (
	testBox     = "build-x"
	testAddress = "100.64.0.2"
	selfAddress = "100.64.0.1"
)

var errSocket = errors.New("dial unix /var/run/tailscaled.socket: connect: no such file")

func peer(host, dnsName string, addresses ...string) *ipnstate.PeerStatus {
	ips := make([]netip.Addr, 0, len(addresses))
	for _, address := range addresses {
		ips = append(ips, netip.MustParseAddr(address))
	}

	return &ipnstate.PeerStatus{HostName: host, DNSName: dnsName, TailscaleIPs: ips}
}

// tailnet is this machine, called laptop, and the given peers.
func tailnet(peers ...*ipnstate.PeerStatus) *ipnstate.Status {
	status := &ipnstate.Status{
		Self: peer("laptop", "laptop.tail1234.ts.net.", selfAddress, "fd7a:115c:a1e0::1"),
		Peer: map[key.NodePublic]*ipnstate.PeerStatus{},
	}

	for _, each := range peers {
		status.Peer[key.NewNode().Public()] = each
	}

	return status
}

func TestPeerMatchesHostOrDNSName(t *testing.T) {
	t.Parallel()

	for _, check := range []struct {
		peer *ipnstate.PeerStatus
		want bool
	}{
		{peer(testBox, ""), true},
		{peer("BUILD-X", ""), true},
		{peer("", testBox+".tail1234.ts.net."), true},
		{peer("", testBox+".tail1234.ts.net"), true},
		{peer("build-x-1", "build-x-1.tail1234.ts.net."), false},
		{peer("other", "other.tail1234.ts.net."), false},
		{nil, false},
	} {
		if got := peerMatches(check.peer, testBox); got != check.want {
			t.Fatalf("peerMatches(%+v) = %v, want %v", check.peer, got, check.want)
		}
	}

	if peerMatches(peer(testBox, ""), "") {
		t.Fatal("an empty name matched")
	}
}

func TestPeerStateFindsTheBox(t *testing.T) {
	t.Parallel()

	state, err := peerStateIn(tailnet(peer(testBox, "", "fd7a:115c:a1e0::2", testAddress)), testBox)
	if err != nil || state != testAddress {
		t.Fatalf("state = %q, err = %v", state, err)
	}
}

func TestPeerStateAbsentAndSelf(t *testing.T) {
	t.Parallel()

	state, err := peerStateIn(tailnet(peer("other", "", testAddress)), testBox)
	if err != nil || state != PeerAbsent {
		t.Fatalf("absent: state = %q, err = %v", state, err)
	}

	state, err = peerStateIn(tailnet(), "laptop")
	if err != nil || state != PeerSelf {
		t.Fatalf("self by name: state = %q, err = %v", state, err)
	}

	// A peer entry with this machine's own address is this machine too, so a
	// box name can never point ssh back at the laptop.
	state, err = peerStateIn(tailnet(peer(testBox, "", selfAddress)), testBox)
	if err != nil || state != PeerSelf {
		t.Fatalf("self by address: state = %q, err = %v", state, err)
	}
}

func TestPeerStateRefusesAmbiguousOrAddresslessMachines(t *testing.T) {
	t.Parallel()

	_, err := peerStateIn(tailnet(peer(testBox, "", testAddress), peer(testBox, "", "100.64.0.3")), testBox)
	if !errors.Is(err, abort.ErrAbort) || !strings.Contains(err.Error(), "more than one machine") {
		t.Fatalf("two matches: err = %v", err)
	}

	_, err = peerStateIn(tailnet(peer(testBox, "", "fd7a:115c:a1e0::2")), testBox)
	if !errors.Is(err, abort.ErrAbort) || !strings.Contains(err.Error(), "IPv4") {
		t.Fatalf("no IPv4: err = %v", err)
	}
}

func TestSelfIPv4FallsBackToTheStatusAddresses(t *testing.T) {
	t.Parallel()

	if address, ok := selfIPv4(tailnet()); !ok || address != selfAddress {
		t.Fatalf("from self: %q %v", address, ok)
	}

	status := &ipnstate.Status{TailscaleIPs: []netip.Addr{
		netip.MustParseAddr("fd7a:115c:a1e0::1"),
		netip.MustParseAddr(selfAddress),
	}}
	if address, ok := selfIPv4(status); !ok || address != selfAddress {
		t.Fatalf("from status: %q %v", address, ok)
	}

	if _, ok := selfIPv4(nil); ok {
		t.Fatal("nil status has an address")
	}
}

func TestFinishTailscaleErrorAddsTheStartHint(t *testing.T) {
	t.Parallel()

	worded := abort.New("Tailscale has more than one machine named " + testBox + ".")
	if !errors.Is(finishTailscaleError(worded), worded) {
		t.Fatal("an abort error was rewrapped")
	}

	var failed *abort.Error

	err := finishTailscaleError(retryTailscale(errSocket.Error()))
	if !errors.As(err, &failed) {
		t.Fatalf("err = %#v", err)
	}

	if failed.Headline != "Tailscale is not up on this machine, so its machines cannot be checked" ||
		failed.Detail != errSocket.Error() ||
		failed.Hint != "Start Tailscale on this machine and log in." {
		t.Fatalf("failure = %+v", failed)
	}

	if finishTailscaleError(nil) != nil {
		t.Fatal("nil became an error")
	}
}

func TestRetryErrorIsRetryable(t *testing.T) {
	t.Parallel()

	if !errors.Is(retryTailscale("state is Stopped"), errTailscaleRetry) {
		t.Fatal("not retryable")
	}
}

func TestPeerTagsComeFromTheOneMatchingMachine(t *testing.T) {
	t.Parallel()

	tagged := peer(testBox, "", testAddress)
	tags := views.SliceOf([]string{"tag:buildbox"})
	tagged.Tags = &tags

	if got := peerTagsIn(tailnet(tagged), testBox); len(got) != 1 || got[0] != "tag:buildbox" {
		t.Fatalf("tags = %v", got)
	}

	if got := peerTagsIn(tailnet(peer(testBox, "", testAddress)), testBox); got != nil {
		t.Fatalf("untagged = %v", got)
	}

	if got := peerTagsIn(tailnet(tagged, peer(testBox, "", "100.64.0.3")), testBox); got != nil {
		t.Fatalf("ambiguous = %v", got)
	}
}

func TestDescribeReadsTheTailnetAndThisUser(t *testing.T) {
	t.Parallel()

	status := tailnet()
	status.CurrentTailnet = &ipnstate.TailnetStatus{Name: "example.com", MagicDNSEnabled: true}
	status.Self.UserID = 7
	status.Self.CapMap = tailcfg.NodeCapMap{nodecap.Admin: nil}
	status.User = map[tailcfg.UserID]tailcfg.UserProfile{7: {LoginName: "ada@example.com"}}

	got := describeIn(status)
	want := Tailnet{Name: "example.com", Login: "ada@example.com", Admin: true, MagicDNS: true}

	if got != want {
		t.Fatalf("got %+v want %+v", got, want)
	}

	if describeIn(&ipnstate.Status{}) != (Tailnet{}) {
		t.Fatal("an empty status should describe nothing")
	}
}
