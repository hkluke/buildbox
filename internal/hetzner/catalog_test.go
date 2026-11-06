package hetzner

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"

	"github.com/hkluke/buildbox/internal/abort"

	"github.com/hkluke/buildbox/internal/config"
)

const (
	testCurrency    = "EUR"
	testZone        = hcloud.NetworkZone("eu-central")
	cityFalkenstein = "Falkenstein"
	fsnLatitude     = 50.47612
	fsnLongitude    = 12.370071
	includedTraffic = uint64(20_000_000_000_000)
)

func TestAssembleCatalogAvailabilityAndPrice(t *testing.T) {
	t.Parallel()

	location := &hcloud.Location{
		Name:        config.SeedLocation,
		City:        cityFalkenstein,
		Country:     "DE",
		NetworkZone: testZone,
		Latitude:    fsnLatitude,
		Longitude:   fsnLongitude,
	}
	open := serverType(config.DefaultType, true, true, "0.0080000000", "4.9900000000", location)
	closed := serverType("ccx13", false, false, "0.0200000000", "12.4900000000", location)
	closed.Category = "dedicated"
	past := time.Now().Add(-time.Hour)
	retired := serverType("cx11", true, false, "0.0050000000", "3.0000000000", location)
	retired.Locations[0].DeprecatableResource = hcloud.DeprecatableResource{
		Deprecation: &hcloud.DeprecationInfo{Announced: past, UnavailableAfter: past},
	}

	cat := AssembleCatalog(
		[]*hcloud.Location{location},
		[]*hcloud.ServerType{open, closed, retired},
		hcloud.Pricing{Currency: testCurrency},
		&hcloud.Firewall{
			Name: FirewallName,
			Rules: []hcloud.FirewallRule{{
				Direction: hcloud.FirewallRuleDirectionIn,
				Protocol:  hcloud.FirewallRuleProtocolUDP,
				Port:      new("41641"),
			}},
		},
		[]*hcloud.Image{
			{Name: "ubuntu-26.04", Description: "Ubuntu 26.04", Architecture: "x86"},
			{Name: "debian-13", Description: "Debian 13", Architecture: "x86"},
		},
		time.Now(),
	)

	assertCatalog(t, cat)
}

// Hetzner returns locations in its own order. init shows Germany first, then
// Finland, the United States, and Singapore, each country sorted by name,
// and any other country after them.
func TestLocationOffersOrderByRegionThenName(t *testing.T) {
	t.Parallel()

	offers := locationOffers([]*hcloud.Location{
		{Name: "sin", Country: "SG"},
		{Name: "hil", Country: "US"},
		{Name: "nbg1", Country: "DE"},
		{Name: "hel1", Country: "FI"},
		{Name: "ash", Country: "US"},
		{Name: "fsn1", Country: "DE"},
		{Name: "tyo1", Country: "JP"},
	}, nil)

	names := make([]string, 0, len(offers))
	for _, offer := range offers {
		names = append(names, offer.Name)
	}

	want := []string{"fsn1", "nbg1", "hel1", "ash", "hil", "sin", "tyo1"}
	if !slices.Equal(names, want) {
		t.Fatalf("order = %v, want %v", names, want)
	}
}

func assertCatalog(t *testing.T, cat Catalog) {
	t.Helper()

	assertLocationCounts(t, cat)
	assertCreatablePlace(t, cat)
	assertFirewallReady(t, cat)
	assertUbuntuImage(t, cat)
}

func assertLocationCounts(t *testing.T, cat Catalog) {
	t.Helper()

	if len(cat.Locations) != 1 || cat.Locations[0].Available != 1 || cat.Locations[0].Unavailable != 2 {
		t.Fatalf("locations = %+v", cat.Locations)
	}

	// The map places each location by where Hetzner says it is.
	if cat.Locations[0].Latitude != fsnLatitude || cat.Locations[0].Longitude != fsnLongitude {
		t.Fatalf("location at %v, %v", cat.Locations[0].Latitude, cat.Locations[0].Longitude)
	}
}

func assertCreatablePlace(t *testing.T, cat Catalog) {
	t.Helper()

	_, place, found := cat.Place(config.DefaultType, config.SeedLocation)
	if !found || !place.Creatable || !place.Recommended || place.Included != includedTraffic {
		t.Fatalf("place = %+v found=%v", place, found)
	}

	_, retired, listed := cat.Place("cx11", config.SeedLocation)
	if !listed || retired.Creatable {
		t.Fatalf("retired = %+v listed=%v", retired, listed)
	}
}

func assertFirewallReady(t *testing.T, cat Catalog) {
	t.Helper()

	if !cat.Firewall.Exists || !cat.Firewall.Ready {
		t.Fatalf("firewall = %+v", cat.Firewall)
	}
}

func TestFirewallStatusMissingAndWider(t *testing.T) {
	t.Parallel()

	if got := firewallStatus(nil); got.Exists {
		t.Fatalf("missing = %+v", got)
	}

	wider := firewallStatus(&hcloud.Firewall{
		Name: FirewallName,
		Rules: []hcloud.FirewallRule{
			{Direction: hcloud.FirewallRuleDirectionIn, Protocol: hcloud.FirewallRuleProtocolUDP, Port: new("41641")},
			{Direction: hcloud.FirewallRuleDirectionIn, Protocol: hcloud.FirewallRuleProtocolTCP, Port: new("22")},
		},
	})
	if !wider.Exists || wider.Ready || wider.Note != "inbound tcp port 22" {
		t.Fatalf("wider = %+v", wider)
	}
}

func assertUbuntuImage(t *testing.T, cat Catalog) {
	t.Helper()

	if len(cat.Images) != 1 || cat.Images[0].Detail != "Ubuntu 26.04 · x86" {
		t.Fatalf("images = %+v", cat.Images)
	}
}

func serverType(
	name string,
	available bool,
	recommended bool,
	hourly string,
	monthly string,
	location *hcloud.Location,
) *hcloud.ServerType {
	return &hcloud.ServerType{
		Name:         name,
		Category:     "cost_optimized",
		Cores:        2,
		Memory:       4,
		Disk:         40,
		StorageType:  hcloud.StorageTypeLocal,
		CPUType:      hcloud.CPUTypeShared,
		Architecture: hcloud.ArchitectureX86,
		Locations: []hcloud.ServerTypeLocation{{
			Location:    location,
			Available:   available,
			Recommended: recommended,
		}},
		Pricings: []hcloud.ServerTypeLocationPricing{{
			Location:        location,
			Hourly:          hcloud.Price{Gross: hourly},
			Monthly:         hcloud.Price{Gross: monthly},
			IncludedTraffic: includedTraffic,
		}},
	}
}

func TestLoadCatalogNamesTheTimeoutWhenTheAPIIsSlow(t *testing.T) {
	t.Parallel()

	// A context already past its deadline stands in for an API that never
	// answers; nothing is sent.
	ctx, cancel := context.WithDeadline(t.Context(), time.Now())
	defer cancel()

	client := hcloud.NewClient(hcloud.WithToken("token"), hcloud.WithEndpoint("http://127.0.0.1:1"))

	_, err := LoadCatalog(ctx, client)
	if err == nil || !strings.Contains(err.Error(), "did not answer within 30s") {
		t.Fatalf("err = %v", err)
	}
}

// LoadPrices stops after the three calls price needs, so a missing firewall
// or a slow image list never gets in the way of a price check.
func TestLoadPricesSkipsTheFirewallAndImages(t *testing.T) {
	t.Parallel()

	var (
		guard sync.Mutex
		paths []string
	)

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		guard.Lock()
		defer guard.Unlock()

		paths = append(paths, request.URL.Path)

		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)

	client := hcloud.NewClient(hcloud.WithToken("token"), hcloud.WithEndpoint(server.URL))

	cat, err := LoadPrices(t.Context(), client)
	if err != nil || cat.Firewall.Exists || len(cat.Images) != 0 {
		t.Fatalf("catalog = %+v err = %v", cat, err)
	}

	guard.Lock()
	defer guard.Unlock()

	want := []string{"/locations", "/server_types", "/pricing"}
	if !slices.Equal(paths, want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
}

func TestCreateFirewallExplainsAReadOnlyToken(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusForbidden)
		_, _ = writer.Write([]byte(`{"error":{"code":"forbidden","message":"insufficient permissions"}}`))
	}))
	t.Cleanup(server.Close)

	client := hcloud.NewClient(hcloud.WithToken("token"), hcloud.WithEndpoint(server.URL))

	var failed *abort.Error

	err := CreateFirewall(t.Context(), client)
	if !errors.As(err, &failed) || !strings.Contains(failed.Hint, "Read & Write") {
		t.Fatalf("err = %#v", err)
	}
}
