package hetzner

import (
	"strings"
	"testing"
)

func TestTailscaleFirewallPassesTheCreateCheck(t *testing.T) {
	t.Parallel()

	opts := tailscaleFirewallOpts()
	if opts.Name != FirewallName || opts.Labels[LabelKey] != LabelValue {
		t.Fatalf("opts = %+v", opts)
	}

	bad, inbound := inboundFirewallProblems(opts.Rules)
	if len(bad) != 0 || inbound != 1 {
		t.Fatalf("bad = %v, inbound = %d", bad, inbound)
	}

	sources := make([]string, 0, len(opts.Rules[0].SourceIPs))
	for _, source := range opts.Rules[0].SourceIPs {
		sources = append(sources, source.String())
	}

	if strings.Join(sources, " ") != "0.0.0.0/0 ::/0" {
		t.Fatalf("sources = %v", sources)
	}
}
