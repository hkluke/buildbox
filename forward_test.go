package main

import (
	"net"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/hkluke/buildbox/internal/config"
)

const (
	testPair = "8000:8001"
	testPort = "5432"
)

func TestForwardParsesTheNameThenThePorts(t *testing.T) {
	t.Parallel()

	want := []portPair{{local: 8000, remote: 8001}, {local: 5432, remote: 5432}}

	for _, args := range [][]string{
		{testBox, testPair, testPort},
		{flagName, testBox, testPair, testPort},
		{flagNameEq + testBox, testPair, testPort},
	} {
		name, pairs, err := parseForwardArgs(args)
		if err != nil || name != testBox || !slices.Equal(pairs, want) {
			t.Fatalf("%v: name = %q, pairs = %v, err = %v", args, name, pairs, err)
		}
	}
}

func TestForwardRefusesWhatItCannotForward(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		nil,
		{testBox},
		{testBox, "web"},
		{testBox, "0"},
		{testBox, "70000"},
		{testBox, "8000:"},
		{testBox, ":8000"},
		{testBox, "1:2:3"},
		{testBox, "8000", "8000:9000"},
		{testBox, "--bogus", "8000"},
		{flagName, testBox, testBox, "8000"},
	} {
		_, _, err := parseForwardArgs(args)
		if err == nil {
			t.Fatalf("%v: expected an error", args)
		}
	}
}

func TestForwardArgsPutTheirOptionsFirst(t *testing.T) {
	t.Parallel()

	args := forwardSSHArgs(testAddress, "kh", []portPair{{local: 8000, remote: 8001}, {local: 5432, remote: 5432}})
	joined := strings.Join(args, " ")

	// ssh keeps the first value of an option, so the forward's own must
	// come before the shared ones.
	if strings.Index(joined, "ServerAliveCountMax=3 ") > strings.Index(joined, "ServerAliveCountMax=40") {
		t.Fatalf("shared options first:\n%s", joined)
	}

	for _, want := range []string{
		"-N -n ",
		"-o ExitOnForwardFailure=yes",
		"-o UserKnownHostsFile=kh",
		"-L localhost:8000:localhost:8001",
		"-L localhost:5432:localhost:5432",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in:\n%s", want, joined)
		}
	}

	if last := args[len(args)-1]; last != config.AgentUser+"@"+testAddress {
		t.Fatalf("last argument %q", last)
	}
}

func TestDescribeSSHLineRewordsChannelFailures(t *testing.T) {
	t.Parallel()

	refused := "channel 2: open failed: connect failed: dial tcp 127.0.0.1:8001: connect: connection refused"
	if got := describeSSHLine(testBox, refused); !strings.Contains(got, "nothing is listening") {
		t.Fatalf("refused: %q", got)
	}

	prohibited := "channel 2: open failed: administratively prohibited: port forwarding is disabled"
	if got := describeSSHLine(testBox, prohibited); !strings.Contains(got, "policy") {
		t.Fatalf("prohibited: %q", got)
	}

	other := "Connection to 100.64.0.2 closed by remote host."
	if got := describeSSHLine(testBox, other); got != other {
		t.Fatalf("other: %q", got)
	}
}

func TestRequirePortsFreeSeesABusyPort(t *testing.T) {
	t.Parallel()

	var listen net.ListenConfig

	listener, err := listen.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = listener.Close() })

	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("address %v", listener.Addr())
	}

	err = requirePortsFree(t.Context(), testBox, []portPair{{local: address.Port, remote: address.Port}})
	if err == nil || !strings.Contains(err.Error(), strconv.Itoa(address.Port)) {
		t.Fatalf("busy port: err = %v", err)
	}
}
