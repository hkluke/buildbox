package wizard

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/hkluke/buildbox/internal/config"
	"github.com/hkluke/buildbox/internal/hetzner"
)

const (
	mapTestWidth  = 104
	mapTestHeight = 40
	fallbackW     = 60
	fallbackH     = 24
	worldRows     = 131
	worldDigits   = 90
	locNuremberg  = "nbg1"
	locAshburn    = "ash"
	locHillsboro  = "hil"
	locSingapore  = "sin"
	zoneEUCentral = "eu-central"
)

// hetznerLocations are Hetzner's locations as the API listed them on
// 2026-10-31, with Singapore sold out.
func hetznerLocations() []hetzner.LocationOffer {
	return []hetzner.LocationOffer{
		{
			Name: config.SeedLocation, City: "Falkenstein", Country: "DE", NetworkZone: zoneEUCentral,
			Latitude: 50.4761, Longitude: 12.3701, Available: 14, Unavailable: 12,
		},
		{
			Name: locNuremberg, City: "Nuremberg", Country: "DE", NetworkZone: zoneEUCentral,
			Latitude: 49.4521, Longitude: 11.0767, Available: 14, Unavailable: 12,
		},
		{
			Name: locHelsinki, City: "Helsinki", Country: "FI", NetworkZone: zoneEUCentral,
			Latitude: 60.1699, Longitude: 24.9384, Available: 14, Unavailable: 12,
		},
		{
			Name: locAshburn, City: "Ashburn, VA", Country: "US", NetworkZone: "us-east",
			Latitude: 39.0458, Longitude: -77.4871, Available: 11,
		},
		{
			Name: locHillsboro, City: "Hillsboro, OR", Country: "US", NetworkZone: "us-west",
			Latitude: 45.5422, Longitude: -122.9519, Available: 11,
		},
		{
			Name: locSingapore, City: "Singapore", Country: "SG", NetworkZone: "ap-southeast",
			Latitude: 1.2833, Longitude: 103.8333, Unavailable: 18,
		},
	}
}

func worldScreen(location string, width, height int) Model {
	wiz := screenAt(stepLocation)
	wiz.cat.Locations = hetznerLocations()
	wiz.location = location
	wiz.suggestLocation = config.SeedLocation
	wiz.width, wiz.height = width, height

	return wiz
}

func hasBraille(view string) bool {
	return strings.ContainsFunc(view, func(char rune) bool { return char > brailleBase && char <= brailleBase+0xFF })
}

func TestTheLandBitmapKnowsLandFromSea(t *testing.T) {
	t.Parallel()

	land := readLand()
	if len(land) != worldRows {
		t.Fatalf("%d rows", len(land))
	}

	for _, line := range land {
		if len(line) != worldDigits {
			t.Fatalf("line of %d digits", len(line))
		}
	}

	places := []struct {
		name     string
		lon, lat float64
		land     bool
	}{
		{"Germany", 10, 50, true},
		{"Brazil", -50, -10, true},
		{"Australia", 135, -25, true},
		{"the Atlantic", -30, 30, false},
		{"the Pacific", -150, -20, false},
		{"north of the map", 10, 80, false},
	}

	for _, place := range places {
		if land.at(place.lon, place.lat) != place.land {
			t.Fatalf("%s: land = %v", place.name, !place.land)
		}
	}
}

func TestTheMapShrinksToTheRowsLeftAndGoesWhenTooSmall(t *testing.T) {
	t.Parallel()

	if grid, fits := fitMap(mapTestWidth, mapTestHeight); !fits || grid.cols != mapMaxWidth {
		t.Fatalf("grid %+v fits %v", grid, fits)
	}

	if grid, fits := fitMap(mapTestWidth, mapMinRows+1); !fits || grid.rows > mapMinRows+1 || grid.cols >= mapMaxWidth {
		t.Fatalf("grid %+v fits %v", grid, fits)
	}

	if _, fits := fitMap(mapTestWidth, mapMinRows-1); fits {
		t.Fatal("a map below the minimum height fits")
	}
}

func TestNeighbouringLocationsKeepTheirOwnCells(t *testing.T) {
	t.Parallel()

	grid := newMapGrid(mapMaxWidth)
	pins := map[string]mapPin{}
	cells := map[[2]int]bool{}

	for _, pin := range grid.pins(hetznerLocations()) {
		if cells[[2]int{pin.col, pin.row}] || pin.col >= grid.cols || pin.row >= grid.rows {
			t.Fatalf("pin %+v", pin)
		}

		cells[[2]int{pin.col, pin.row}] = true
		pins[pin.offer.Name] = pin
	}

	nuremberg, falkenstein := pins[locNuremberg], pins[config.SeedLocation]
	if nuremberg.row != falkenstein.row || nuremberg.col >= falkenstein.col {
		t.Fatalf("Nuremberg %+v is not west of Falkenstein %+v", nuremberg, falkenstein)
	}

	if pins[locHelsinki].row >= falkenstein.row || pins[locHillsboro].col >= pins[locAshburn].col {
		t.Fatalf("pins %+v", pins)
	}
}

func TestArrowsMoveWestAndEastRoundTheWorld(t *testing.T) {
	t.Parallel()

	moves := []struct {
		from, key, to string
	}{
		{config.SeedLocation, "right", locHelsinki},
		{config.SeedLocation, "left", locNuremberg},
		{locHelsinki, "l", locSingapore},
		{locSingapore, "right", locHillsboro},
		{locHillsboro, "left", locSingapore},
		{locAshburn, "h", locHillsboro},
	}

	for _, move := range moves {
		wiz := worldScreen(move.from, mapTestWidth, mapTestHeight)
		wiz.note = "stale"

		next, _ := wiz.onKey(press(move.key))
		if next.location != move.to || next.note != "" {
			t.Fatalf("%s from %s went to %s", move.key, move.from, next.location)
		}
	}

	down, _ := worldScreen(config.SeedLocation, mapTestWidth, mapTestHeight).onKey(press("j"))
	if down.location != locNuremberg {
		t.Fatalf("j went to %s", down.location)
	}
}

func TestTheLocationScreenShowsTheMapWhenItFits(t *testing.T) {
	t.Parallel()

	view := stripANSI(worldScreen(config.SeedLocation, mapTestWidth, mapTestHeight).View())
	if !hasBraille(view) || strings.Count(view, markSelected) != 2 {
		t.Fatalf("no map:\n%s", view)
	}

	assertViewContains(t, view, "fsn1 · Falkenstein, Germany", "▸ fsn1  Falkenstein, Germany", "eu-central",
		"14 of 26 types", "sold out", recommendedTag, "←→ west/east")

	small := stripANSI(worldScreen(config.SeedLocation, fallbackW, fallbackH).View())
	if hasBraille(small) {
		t.Fatalf("a map on a small screen:\n%s", small)
	}

	assertViewContains(t, small, "eu-central · 14 of 26 types")
}

// The map shows only where the whole screen fits; elsewhere the screen is
// the list it was before the map.
func TestTheMapScreenFitsWheneverItShows(t *testing.T) {
	t.Parallel()

	shown := 0

	for width := narrowWidth; width <= 140; width += 4 {
		for height := shortHeight; height <= 50; height++ {
			wiz := worldScreen(locHillsboro, width, height)
			view := wiz.View()

			if !hasBraille(view) {
				if view != wiz.viewPick() {
					t.Fatalf("%dx%d: the list changed", width, height)
				}

				continue
			}

			shown++

			lines := strings.Split(view, "\n")
			if len(lines) > height {
				t.Fatalf("%dx%d: %d lines", width, height, len(lines))
			}

			for _, line := range lines {
				if lipgloss.Width(line) > width {
					t.Fatalf("%dx%d: line of %d:\n%s", width, height, lipgloss.Width(line), stripANSI(view))
				}
			}
		}
	}

	if shown == 0 {
		t.Fatal("the map never showed")
	}
}

// Every location gets its label at the usual width, and no label hides
// another location's marker.
func TestTheLabelNeverHidesAnotherMarker(t *testing.T) {
	t.Parallel()

	for _, offer := range hetznerLocations() {
		for _, cols := range []int{mapMaxWidth, 72, 50} {
			wiz := worldScreen(offer.Name, mapTestWidth, mapTestHeight)
			drawn := stripANSI(strings.Join(wiz.drawMap(newMapGrid(cols)), "\n"))

			// Each mark is on the map once per location, plus once in the legend.
			open, sold := strings.Count(drawn, markOpen), strings.Count(drawn, markSoldOut)
			wantOpen, wantSold := 5, 1

			if offer.Name == locSingapore {
				wantSold = 0
			} else {
				wantOpen = 4
			}

			if open != wantOpen+1 || sold != wantSold+1 || strings.Count(drawn, markSelected) != 2 {
				t.Fatalf("%s at %d: %d open, %d sold out:\n%s", offer.Name, cols, open, sold, drawn)
			}

			if label := offer.Name + " · " + locationLabel(offer); cols == mapMaxWidth && !strings.Contains(drawn, label) {
				t.Fatalf("%s has no label:\n%s", offer.Name, drawn)
			}
		}
	}
}
