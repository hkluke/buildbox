package hetzner

import (
	"slices"
	"testing"
)

const (
	offerFSN       = "fsn1"
	offerASH       = "ash"
	offerCheap     = "cx23"
	offerARM       = "cax21"
	offerDedicated = "ccx13"
	cpuShared      = "shared"
	cpuDedicated   = "dedicated"
	archX86        = "x86"
	archARM        = "arm"
)

// offersCatalog is three types in two locations: a cheap shared one
// everywhere, an ARM one sold out in Falkenstein and not offered in
// Ashburn, and a dedicated one everywhere. The ARM one would be cheaper
// than the dedicated one if it could be created, so it shows that a
// sold-out price does not sort.
func offersCatalog() Catalog {
	return Catalog{
		Currency:  testCurrency,
		Locations: []LocationOffer{{Name: offerFSN}, {Name: offerASH}},
		Machines: []MachineOffer{
			{
				Name: offerARM, CPU: cpuShared, Arch: archARM, Cores: 4, Memory: 8, Disk: 80,
				Places: map[string]PlaceOffer{offerFSN: {MonthlyGross: "6.49"}},
			},
			{
				Name: offerDedicated, CPU: cpuDedicated, Arch: archX86, Cores: 2, Memory: 8, Disk: 80,
				Places: map[string]PlaceOffer{
					offerFSN: {Creatable: true, MonthlyGross: "12.49"},
					offerASH: {Creatable: true, MonthlyGross: "14.99"},
				},
			},
			{
				Name: offerCheap, CPU: cpuShared, Arch: archX86, Cores: 2, Memory: 4, Disk: 40,
				Places: map[string]PlaceOffer{
					offerFSN: {Creatable: true, MonthlyGross: "3.79"},
					offerASH: {Creatable: true, MonthlyGross: "4.99"},
				},
			},
		},
	}
}

func offerNames(offers []MachineOffer) []string {
	names := make([]string, 0, len(offers))
	for _, offer := range offers {
		names = append(names, offer.Name)
	}

	return names
}

func TestOffersFiltersExactlyOrFromAMinimumAndSortByPrice(t *testing.T) {
	t.Parallel()

	cat := offersCatalog()

	cases := []struct {
		filter Filter
		want   []string
	}{
		{Filter{}, []string{offerCheap, offerDedicated, offerARM}},
		{Filter{Cores: []Span[int]{{Low: 2, High: 2}}}, []string{offerCheap, offerDedicated}},
		{Filter{MinCores: 4}, []string{offerARM}},
		{Filter{Memory: []Span[float32]{{Low: 8, High: 8}}}, []string{offerDedicated, offerARM}},
		{
			Filter{Memory: []Span[float32]{{Low: 4, High: 4}, {Low: 8, High: 8}}},
			[]string{offerCheap, offerDedicated, offerARM},
		},
		{Filter{Memory: []Span[float32]{{Low: 5, High: 9}}}, []string{offerDedicated, offerARM}},
		{Filter{MinMemory: 5}, []string{offerDedicated, offerARM}},
		{Filter{Memory: []Span[float32]{{Low: 8, High: 8}}, CPU: cpuDedicated}, []string{offerDedicated}},
		{Filter{Disk: []Span[int]{{Low: 40, High: 40}}}, []string{offerCheap}},
		{Filter{Disk: []Span[int]{{Low: 50, High: 100}}}, []string{offerDedicated, offerARM}},
		{Filter{MinDisk: 80, Arch: archX86}, []string{offerDedicated}},
		{Filter{MaxCores: 2}, []string{offerCheap, offerDedicated}},
		{Filter{MinMemory: 8, MaxMemory: 8}, []string{offerDedicated, offerARM}},
		{Filter{MaxDisk: 40}, []string{offerCheap}},
		{Filter{Locations: []string{offerASH}}, []string{offerCheap, offerDedicated}},
		{Filter{Memory: []Span[float32]{{Low: 6, High: 6}}}, []string{}},
		{Filter{MinCores: 8}, []string{}},
	}

	for _, testCase := range cases {
		if got := offerNames(cat.Offers(testCase.filter)); !slices.Equal(got, testCase.want) {
			t.Fatalf("%+v: got %v, want %v", testCase.filter, got, testCase.want)
		}
	}
}

func TestCheapestNamesEveryLocationAtTheLowestPrice(t *testing.T) {
	t.Parallel()

	cat := offersCatalog()

	machine, places := cat.Cheapest(Filter{})
	if machine.Name != offerCheap || len(places) != 1 || places[0].Name != offerFSN {
		t.Fatalf("cheapest = %s in %+v", machine.Name, places)
	}

	if _, none := cat.Cheapest(Filter{MinCores: 8}); len(none) != 0 {
		t.Fatalf("no match = %+v", none)
	}

	// The same price in two places names both, in catalog order.
	cat.Machines[1].Places[offerASH] = PlaceOffer{Creatable: true, MonthlyGross: "12.49"}

	machine, places = cat.Cheapest(Filter{CPU: cpuDedicated})
	if machine.Name != offerDedicated || len(places) != 2 || places[0].Name != offerFSN || places[1].Name != offerASH {
		t.Fatalf("tie = %s in %+v", machine.Name, places)
	}
}

func TestShownKeepsCatalogOrder(t *testing.T) {
	t.Parallel()

	cat := offersCatalog()

	shown := cat.Shown(Filter{Locations: []string{offerASH, offerFSN}})
	if len(shown) != 2 || shown[0].Name != offerFSN || shown[1].Name != offerASH {
		t.Fatalf("shown = %+v", shown)
	}

	if all := cat.Shown(Filter{}); len(all) != len(cat.Locations) {
		t.Fatalf("all = %+v", all)
	}
}

func TestAvailabilityThreeWays(t *testing.T) {
	t.Parallel()

	cat := offersCatalog()
	arm := cat.Machines[0]

	if got := arm.Availability(offerFSN); got != SoldOut {
		t.Fatalf("sold out = %q", got)
	}

	if got := arm.Availability(offerASH); got != NotOffered {
		t.Fatalf("not offered = %q", got)
	}

	if got := cat.Machines[2].Availability(offerFSN); got != Available {
		t.Fatalf("available = %q", got)
	}
}
