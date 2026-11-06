package hetzner

import (
	"cmp"
	"math"
	"slices"
	"strings"
)

// The availability of a server type at one location, as price --json
// spells it.
const (
	Available  = "available"
	SoldOut    = "sold_out"
	NotOffered = "not_offered"
)

// Span is one asked-for size: exact when both ends are equal, a range
// from Low to High inclusive otherwise.
type Span[Size int | float32] struct {
	Low  Size
	High Size
}

// Filter narrows the catalog for price. Cores, Memory, and Disk are spans,
// any of which matches; the Min and Max fields bound a size from either
// side; empty or zero means no bound. Arch and CPU are exact; empty means
// both. Locations limits the places shown; empty means every location in
// the catalog.
type Filter struct {
	Cores     []Span[int]
	MinCores  int
	MaxCores  int
	Memory    []Span[float32]
	MinMemory float32
	MaxMemory float32
	Disk      []Span[int]
	MinDisk   int
	MaxDisk   int
	Arch      string
	CPU       string
	Locations []string
}

// Shown is the locations price shows, in catalog order: those named in
// the filter, or all of them.
func (cat Catalog) Shown(filter Filter) []LocationOffer {
	if len(filter.Locations) == 0 {
		return cat.Locations
	}

	shown := make([]LocationOffer, 0, len(filter.Locations))

	for _, location := range cat.Locations {
		if slices.Contains(filter.Locations, location.Name) {
			shown = append(shown, location)
		}
	}

	return shown
}

// Offers is the machines that meet the filter and are offered in at least
// one shown location, cheapest first by the lowest monthly price where
// the type can be created, then by name. A type sold out everywhere shown
// has no price to sort by and goes last.
func (cat Catalog) Offers(filter Filter) []MachineOffer {
	shown := cat.Shown(filter)
	offers := make([]MachineOffer, 0, len(cat.Machines))

	for _, machine := range cat.Machines {
		if machine.fits(filter) && machine.offeredIn(shown) {
			offers = append(offers, machine)
		}
	}

	slices.SortStableFunc(offers, func(left, right MachineOffer) int {
		byPrice := cmp.Compare(left.lowestMonthly(shown), right.lowestMonthly(shown))
		if byPrice != 0 {
			return byPrice
		}

		return strings.Compare(left.Name, right.Name)
	})

	return offers
}

// Cheapest is the first of Offers and the shown locations where it costs
// the least, in catalog order. The locations are empty when nothing
// matches.
func (cat Catalog) Cheapest(filter Filter) (MachineOffer, []LocationOffer) {
	offers := cat.Offers(filter)
	if len(offers) == 0 {
		return MachineOffer{}, nil
	}

	shown := cat.Shown(filter)
	machine := offers[0]
	lowest := machine.lowestMonthly(shown)
	places := make([]LocationOffer, 0, len(shown))

	for _, location := range shown {
		place, ok := machine.Places[location.Name]
		if ok && place.Creatable && MonthlyValue(place.MonthlyGross) == lowest {
			places = append(places, location)
		}
	}

	return machine, places
}

// Availability of the machine at the location called name.
func (machine MachineOffer) Availability(name string) string {
	place, ok := machine.Places[name]

	switch {
	case !ok:
		return NotOffered
	case !place.Creatable:
		return SoldOut
	default:
		return Available
	}
}

func (machine MachineOffer) fits(filter Filter) bool {
	if !sizeFits(machine.Cores, filter.Cores, filter.MinCores, filter.MaxCores) ||
		!sizeFits(machine.Memory, filter.Memory, filter.MinMemory, filter.MaxMemory) ||
		!sizeFits(machine.Disk, filter.Disk, filter.MinDisk, filter.MaxDisk) {
		return false
	}

	if filter.Arch != "" && machine.Arch != filter.Arch {
		return false
	}

	return filter.CPU == "" || machine.CPU == filter.CPU
}

// sizeFits applies the spans, any of which may match, then a lower and an
// upper bound; an empty list or a zero bound means no bound.
func sizeFits[Size int | float32](have Size, spans []Span[Size], least, most Size) bool {
	return (len(spans) == 0 || inAnySpan(have, spans)) && have >= least && (most == 0 || have <= most)
}

func inAnySpan[Size int | float32](have Size, spans []Span[Size]) bool {
	for _, span := range spans {
		if have >= span.Low && have <= span.High {
			return true
		}
	}

	return false
}

func (machine MachineOffer) offeredIn(shown []LocationOffer) bool {
	for _, location := range shown {
		if _, ok := machine.Places[location.Name]; ok {
			return true
		}
	}

	return false
}

func (machine MachineOffer) lowestMonthly(shown []LocationOffer) float64 {
	lowest := math.MaxFloat64

	for _, location := range shown {
		if place, ok := machine.Places[location.Name]; ok && place.Creatable {
			lowest = min(lowest, MonthlyValue(place.MonthlyGross))
		}
	}

	return lowest
}
