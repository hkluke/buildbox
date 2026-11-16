package hetzner

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"

	"github.com/hkluke/buildbox/internal/config"
)

// The image is not a setting, so the create request is the one place that
// decides what a box boots.
func TestCreateServerAsksForTheOneImage(t *testing.T) {
	t.Parallel()

	var (
		guard sync.Mutex
		sent  struct {
			Image string `json:"image"`
		}
	)

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")

		if request.Method == http.MethodGet {
			_, _ = writer.Write([]byte(`{"firewalls":[{"id":1,"name":"` + FirewallName + `"}]}`))

			return
		}

		guard.Lock()
		defer guard.Unlock()

		_ = json.NewDecoder(request.Body).Decode(&sent)

		writer.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = writer.Write([]byte(`{"error":{"code":"invalid_input","message":"refused"}}`))
	}))
	t.Cleanup(server.Close)

	client := hcloud.NewClient(hcloud.WithToken("token"), hcloud.WithEndpoint(server.URL))

	created, err := CreateServer(t.Context(), client, config.Config{
		FirewallName: FirewallName,
		ServerType:   config.DefaultType,
		ServerLoc:    config.SeedLocation,
	}, "box", "#cloud-config\n")
	if err == nil || created {
		t.Fatalf("created = %v err = %v", created, err)
	}

	guard.Lock()
	defer guard.Unlock()

	if sent.Image != config.Image {
		t.Fatalf("image = %q, want %q", sent.Image, config.Image)
	}
}

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
