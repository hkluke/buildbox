package hetzner

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"

	"github.com/hkluke/buildbox/internal/config"
)

// Catalog is the Hetzner data init can show without keeping hcloud types.
// Prices on a server type have no currency; that comes from the pricing call.
type Catalog struct {
	Currency  string
	Locations []LocationOffer
	Machines  []MachineOffer
	Firewall  FirewallState
	Images    []ImageOffer
}

// LocationOffer is one location, where it is, and how many types it can
// create.
type LocationOffer struct {
	Name        string
	City        string
	Country     string
	Description string
	NetworkZone string
	Latitude    float64
	Longitude   float64
	Available   int
	Unavailable int
}

// MachineOffer is one server type and its offer at each location.
type MachineOffer struct {
	Name     string
	Category string
	CPU      string
	Arch     string
	Storage  string
	Cores    int
	Memory   float32
	Disk     int
	Places   map[string]PlaceOffer
}

// PlaceOffer is a server type at one location: whether it can be created,
// and its gross price.
type PlaceOffer struct {
	Creatable    bool
	Recommended  bool
	Deprecated   bool
	HourlyGross  string
	MonthlyGross string
	Included     uint64
}

// FirewallState is buildbox's own firewall as init found it. A missing one is
// created when the review is confirmed.
type FirewallState struct {
	Exists bool
	Ready  bool
	Note   string
}

// ImageOffer is one image buildbox can provision, with its architectures.
type ImageOffer struct {
	Name   string
	Detail string
}

// errNoAnswer is a catalog load that ran out of time.
var errNoAnswer = errors.New("the Hetzner API did not answer")

// LoadCatalog reads locations, server types, prices, the firewall, and the
// Ubuntu images in one go, within catalogTimeout.
func LoadCatalog(ctx context.Context, client *hcloud.Client) (Catalog, error) {
	return loadWithin(ctx, client, fetchCatalog)
}

// LoadPrices reads locations, server types, and prices, within
// catalogTimeout. Firewall and Images stay empty; price needs neither.
func LoadPrices(ctx context.Context, client *hcloud.Client) (Catalog, error) {
	return loadWithin(ctx, client, fetchPrices)
}

func loadWithin(
	ctx context.Context,
	client *hcloud.Client,
	fetch func(context.Context, *hcloud.Client) (Catalog, error),
) (Catalog, error) {
	callCtx, cancel := context.WithTimeout(ctx, catalogTimeout)
	defer cancel()

	cat, err := fetch(callCtx, client)
	if errors.Is(err, context.DeadlineExceeded) {
		return Catalog{}, fmt.Errorf("%w within %s", errNoAnswer, catalogTimeout)
	}

	return cat, err
}

// apiOffers is what the three calls every catalog needs return.
type apiOffers struct {
	locations   []*hcloud.Location
	serverTypes []*hcloud.ServerType
	pricing     hcloud.Pricing
}

func fetchOffers(callCtx context.Context, client *hcloud.Client) (apiOffers, error) {
	locations, err := client.Location.All(callCtx)
	if err != nil {
		return apiOffers{}, fmt.Errorf("could not list Hetzner locations: %w", err)
	}

	serverTypes, err := client.ServerType.All(callCtx)
	if err != nil {
		return apiOffers{}, fmt.Errorf("could not list Hetzner server types: %w", err)
	}

	pricing, _, err := client.Pricing.Get(callCtx)
	if err != nil {
		return apiOffers{}, fmt.Errorf("could not read Hetzner prices: %w", err)
	}

	return apiOffers{locations: locations, serverTypes: serverTypes, pricing: pricing}, nil
}

func fetchPrices(callCtx context.Context, client *hcloud.Client) (Catalog, error) {
	offers, err := fetchOffers(callCtx, client)
	if err != nil {
		return Catalog{}, err
	}

	return AssembleCatalog(offers.locations, offers.serverTypes, offers.pricing, nil, nil, time.Now()), nil
}

func fetchCatalog(callCtx context.Context, client *hcloud.Client) (Catalog, error) {
	offers, err := fetchOffers(callCtx, client)
	if err != nil {
		return Catalog{}, err
	}

	firewall, _, err := client.Firewall.GetByName(callCtx, FirewallName)
	if err != nil {
		return Catalog{}, fmt.Errorf("could not read Hetzner firewall %s: %w", FirewallName, err)
	}

	images, err := client.Image.AllWithOpts(callCtx, hcloud.ImageListOpts{
		Type:   []hcloud.ImageType{hcloud.ImageTypeSystem},
		Status: []hcloud.ImageStatus{hcloud.ImageStatusAvailable},
	})
	if err != nil {
		return Catalog{}, fmt.Errorf("could not list Hetzner images: %w", err)
	}

	return AssembleCatalog(offers.locations, offers.serverTypes, offers.pricing, firewall, images, time.Now()), nil
}

// AssembleCatalog builds the catalog from API data. now decides which
// deprecated type locations are already gone.
func AssembleCatalog(
	locations []*hcloud.Location,
	serverTypes []*hcloud.ServerType,
	pricing hcloud.Pricing,
	firewall *hcloud.Firewall,
	images []*hcloud.Image,
	now time.Time,
) Catalog {
	machines := machineOffers(serverTypes, now)

	return Catalog{
		Currency:  pricing.Currency,
		Locations: locationOffers(locations, machines),
		Machines:  machines,
		Firewall:  firewallStatus(firewall),
		Images:    imageOffers(images),
	}
}

func locationOffers(locations []*hcloud.Location, machines []MachineOffer) []LocationOffer {
	counts := placeCounts(machines)
	offers := make([]LocationOffer, 0, len(locations))

	for _, location := range locations {
		if location == nil || location.Name == "" {
			continue
		}

		pair := counts[location.Name]
		offers = append(offers, LocationOffer{
			Name:        location.Name,
			City:        location.City,
			Country:     location.Country,
			Description: location.Description,
			NetworkZone: string(location.NetworkZone),
			Latitude:    location.Latitude,
			Longitude:   location.Longitude,
			Available:   pair[0],
			Unavailable: pair[1],
		})
	}

	slices.SortFunc(offers, compareLocations)

	return offers
}

// regionOrder is the order init lists locations in: Germany, Finland, the
// United States, Singapore. It is the order Hetzner opened them in, which
// also puts the default location first. A country Hetzner adds later follows.
func regionOrder() []string {
	return []string{"DE", "FI", "US", "SG"}
}

// compareLocations keeps the locations of one country together and in a
// fixed order, so the picker reads the same way on every run.
func compareLocations(left, right LocationOffer) int {
	byRegion := cmp.Compare(regionRank(left.Country), regionRank(right.Country))
	if byRegion != 0 {
		return byRegion
	}

	return strings.Compare(left.Name, right.Name)
}

func regionRank(country string) int {
	order := regionOrder()

	rank := slices.Index(order, country)
	if rank < 0 {
		return len(order)
	}

	return rank
}

func placeCounts(machines []MachineOffer) map[string][2]int {
	counts := map[string][2]int{}

	for _, machine := range machines {
		for name, place := range machine.Places {
			pair := counts[name]
			if place.Creatable {
				pair[0]++
			} else {
				pair[1]++
			}

			counts[name] = pair
		}
	}

	return counts
}

func machineOffers(serverTypes []*hcloud.ServerType, now time.Time) []MachineOffer {
	offers := make([]MachineOffer, 0, len(serverTypes))

	for _, serverType := range serverTypes {
		if serverType == nil || serverType.Name == "" {
			continue
		}

		offers = append(offers, MachineOffer{
			Name:     serverType.Name,
			Category: serverType.Category,
			CPU:      string(serverType.CPUType),
			Arch:     string(serverType.Architecture),
			Storage:  string(serverType.StorageType),
			Cores:    serverType.Cores,
			Memory:   serverType.Memory,
			Disk:     serverType.Disk,
			Places:   placesFor(serverType, now),
		})
	}

	slices.SortFunc(offers, func(left, right MachineOffer) int {
		return strings.Compare(left.Name, right.Name)
	})

	return offers
}

// A type can still be listed at a location after it is deprecated. Once
// unavailable_after has passed, hcloud treats that location as gone.
func placesFor(serverType *hcloud.ServerType, now time.Time) map[string]PlaceOffer {
	places := map[string]PlaceOffer{}

	for _, location := range serverType.Locations {
		if location.Location == nil || location.Location.Name == "" {
			continue
		}

		open := locationOpen(location, now)
		places[location.Location.Name] = PlaceOffer{
			Creatable:   open,
			Recommended: location.Recommended && open,
			Deprecated:  location.IsDeprecated(),
		}
	}

	for _, price := range serverType.Pricings {
		if price.Location == nil || price.Location.Name == "" {
			continue
		}

		place, ok := places[price.Location.Name]
		if !ok {
			continue
		}

		place.HourlyGross = price.Hourly.Gross
		place.MonthlyGross = price.Monthly.Gross
		place.Included = price.IncludedTraffic
		places[price.Location.Name] = place
	}

	return places
}

func locationOpen(location hcloud.ServerTypeLocation, now time.Time) bool {
	if !location.Available {
		return false
	}

	if location.IsDeprecated() && location.UnavailableAfter().Before(now) {
		return false
	}

	return true
}

func firewallStatus(firewall *hcloud.Firewall) FirewallState {
	if firewall == nil {
		return FirewallState{}
	}

	ready, note := firewallNote(firewall.Rules)

	return FirewallState{Exists: true, Ready: ready, Note: note}
}

func firewallNote(rules []hcloud.FirewallRule) (bool, string) {
	bad, inbound := inboundFirewallProblems(rules)
	if len(bad) == 0 && inbound >= 1 {
		return true, "only inbound UDP 41641"
	}

	if len(bad) == 0 {
		return false, "missing inbound UDP 41641"
	}

	return false, strings.Join(bad, ", ")
}

func imageOffers(images []*hcloud.Image) []ImageOffer {
	order := []string{}
	byName := map[string]*imageBuild{}

	for _, image := range images {
		if image == nil || !strings.HasPrefix(image.Name, config.ImagePrefix) || image.IsDeprecated() {
			continue
		}

		item, ok := byName[image.Name]
		if !ok {
			item = &imageBuild{detail: imageDetail(image), archs: map[string]struct{}{}}
			byName[image.Name] = item
			order = append(order, image.Name)
		}

		if image.Architecture != "" {
			item.archs[string(image.Architecture)] = struct{}{}
		}
	}

	slices.Sort(order)
	slices.Reverse(order)

	offers := make([]ImageOffer, 0, len(order))
	for _, name := range order {
		offers = append(offers, ImageOffer{Name: name, Detail: byName[name].line()})
	}

	return offers
}

type imageBuild struct {
	detail string
	archs  map[string]struct{}
}

func (item *imageBuild) line() string {
	if len(item.archs) == 0 {
		return item.detail
	}

	archs := make([]string, 0, len(item.archs))
	for arch := range item.archs {
		archs = append(archs, arch)
	}

	slices.Sort(archs)

	return item.detail + " · " + strings.Join(archs, ", ")
}

func imageDetail(image *hcloud.Image) string {
	if image.Description != "" {
		return image.Description
	}

	if image.OSVersion != "" {
		return "Ubuntu " + image.OSVersion
	}

	return "Ubuntu"
}

// HasLocation reports whether the catalog lists the location called name.
func (cat Catalog) HasLocation(name string) bool {
	_, ok := cat.Location(name)

	return ok
}

// Location returns the location called name.
func (cat Catalog) Location(name string) (LocationOffer, bool) {
	for _, location := range cat.Locations {
		if location.Name == name {
			return location, true
		}
	}

	return LocationOffer{}, false
}

// Place returns a server type and its offer at one location.
func (cat Catalog) Place(serverType, location string) (MachineOffer, PlaceOffer, bool) {
	for _, machine := range cat.Machines {
		if machine.Name != serverType {
			continue
		}

		place, ok := machine.Places[location]
		if !ok {
			return MachineOffer{}, PlaceOffer{}, false
		}

		return machine, place, true
	}

	return MachineOffer{}, PlaceOffer{}, false
}
