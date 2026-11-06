package wizard

import (
	"fmt"
	"math"

	"github.com/hkluke/buildbox/internal/hetzner"
)

const (
	decimalTB = uint64(1_000_000_000_000)
	binaryTB  = uint64(1) << 40
)

type machineRank struct {
	name        string
	recommended bool
	price       float64
}

func pickMachine(cat hetzner.Catalog, location, prefer string) string {
	if _, place, ok := cat.Place(prefer, location); ok && place.Creatable {
		return prefer
	}

	best := machineRank{price: math.MaxFloat64}

	for _, machine := range cat.Machines {
		place, ok := machine.Places[location]
		if !ok || !place.Creatable {
			continue
		}

		candidate := machineRank{
			name:        machine.Name,
			recommended: place.Recommended,
			price:       hetzner.MonthlyValue(place.MonthlyGross),
		}
		if betterMachine(candidate, best) {
			best = candidate
		}
	}

	return best.name
}

func betterMachine(candidate, current machineRank) bool {
	if current.name == "" {
		return true
	}

	if candidate.recommended != current.recommended {
		return candidate.recommended
	}

	if candidate.price != current.price {
		return candidate.price < current.price
	}

	return candidate.name < current.name
}

// priceLabel puts the hourly price, which is what Hetzner bills, before the
// monthly cap, so a box is not mistaken for a monthly commitment. It is the
// compact form, for the right edge of a list row.
func priceLabel(currency string, place hetzner.PlaceOffer) string {
	return hetzner.FormatMoney(currency, place.HourlyGross, hetzner.HourlyFrac) + "/h (max " +
		hetzner.FormatMoney(currency, place.MonthlyGross, hetzner.MonthlyFrac) + "/mo)"
}

// priceSentence spells the billing out, for the hub and the detail pane.
func priceSentence(currency string, place hetzner.PlaceOffer) string {
	return hetzner.FormatMoney(currency, place.HourlyGross, hetzner.HourlyFrac) + " per hour (max " +
		hetzner.FormatMoney(currency, place.MonthlyGross, hetzner.MonthlyFrac) + " per month)"
}

func formatTraffic(amount uint64) string {
	if amount == 0 {
		return ""
	}

	if amount%decimalTB == 0 {
		return fmt.Sprintf("%d TB included", amount/decimalTB)
	}

	if amount%binaryTB == 0 {
		return fmt.Sprintf("%d TiB included", amount/binaryTB)
	}

	tebibytes := float64(amount) / float64(binaryTB)

	return fmt.Sprintf("%.1f TiB included", tebibytes)
}

// countryName spells out the country codes Hetzner uses today; a new one
// shows as its code until it is added here.
func countryName(code string) string {
	switch code {
	case "DE":
		return "Germany"
	case "FI":
		return "Finland"
	case "US":
		return "United States"
	case "SG":
		return "Singapore"
	default:
		return code
	}
}

func locationLabel(location hetzner.LocationOffer) string {
	if location.City != "" && location.Country != "" {
		return location.City + ", " + countryName(location.Country)
	}

	if location.Description != "" {
		return location.Description
	}

	return location.Name
}

func preferredLocation(cat hetzner.Catalog, prefer string) string {
	if cat.HasLocation(prefer) {
		return prefer
	}

	if cat.HasLocation("fsn1") {
		return "fsn1"
	}

	best := ""
	bestCount := -1

	for _, location := range cat.Locations {
		if location.Available > bestCount {
			best = location.Name
			bestCount = location.Available
		}
	}

	return best
}
