package wizard

import (
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hkluke/buildbox/internal/config"
	"github.com/hkluke/buildbox/internal/hetzner"
)

const (
	testCurrency    = "EUR"
	testSSHRule     = "inbound tcp port 22"
	testDest        = "/tmp/buildbox-env"
	locHelsinki     = "hel1"
	includedTraffic = 20 * decimalTB
)

func TestFormatTraffic(t *testing.T) {
	t.Parallel()

	if got := formatTraffic(20 * decimalTB); got != "20 TB included" {
		t.Fatalf("traffic = %q", got)
	}
}

func TestPickMachinePrefersARecommendedOpenType(t *testing.T) {
	t.Parallel()

	cat := hetzner.Catalog{
		Currency:  testCurrency,
		Locations: []hetzner.LocationOffer{falkenstein(1, 2)},
		Machines: []hetzner.MachineOffer{
			machine("ccx13", false, false, "0.0200000000", "12.4900000000"),
			machine("cx11", false, false, "0.0050000000", "3.0000000000"),
			machine(config.DefaultType, true, true, "0.0080000000", "4.9900000000"),
		},
	}

	if got := pickMachine(cat, config.SeedLocation, "ccx13"); got != config.DefaultType {
		t.Fatalf("pick = %q", got)
	}
}

func TestWizardShowsPriceAndSoldOutType(t *testing.T) {
	t.Parallel()

	cat := hetzner.Catalog{
		Currency: testCurrency,
		Locations: []hetzner.LocationOffer{
			falkenstein(1, 1),
			{
				Name: locHelsinki, City: "Helsinki", Country: "FI", NetworkZone: zoneEUCentral,
				Latitude: 60.1699, Longitude: 24.9384,
			},
		},
		Machines: []hetzner.MachineOffer{
			machine("ccx13", false, false, "0.0200000000", "12.4900000000"),
			machine(config.DefaultType, true, true, "0.0080000000", "4.9900000000"),
		},
	}

	seed := config.Config{ServerLoc: config.SeedLocation, ServerType: config.DefaultType}
	wiz := New(seed, testDest, false, adminTailnet(), nil)
	wiz.cat = cat
	wiz.step = stepLocation
	wiz.location = config.SeedLocation
	wiz.width = 88
	wiz.height = 36

	locationView := stripANSI(wiz.View())
	assertViewContains(t, locationView, "fsn1  Falkenstein, Germany", "hel1  Helsinki, Finland", "1 of 2 types",
		"sold out", "fsn1 · Falkenstein, Germany", markSelected, "↑↓ move")

	wiz.step = stepMachine
	wiz.serverType = config.DefaultType

	machineView := stripANSI(wiz.View())
	assertViewContains(
		t,
		machineView,
		"€4.99/mo",
		"€0.0080 per hour",
		"€4.99 per month",
		"ccx13",
		"unavailable",
		"20 TB included",
	)

	t.Log("\n" + locationView + "\n---\n" + machineView)
}

func TestSuggestedLocationAndMachineAreMarked(t *testing.T) {
	t.Parallel()

	wiz := New(config.Config{}, testDest, false, adminTailnet(), nil)
	wiz.cat = hetzner.Catalog{
		Currency:  testCurrency,
		Locations: []hetzner.LocationOffer{falkenstein(1, 0)},
		Machines:  []hetzner.MachineOffer{machine(config.DefaultType, true, true, "0.0080000000", "4.9900000000")},
	}
	wiz.width = 88
	wiz.height = 36
	wiz.location = config.SeedLocation
	wiz.suggestLocation = config.SeedLocation
	wiz.serverType = config.DefaultType
	wiz.suggestMachine = config.DefaultType
	wiz.step = stepLocation

	locationView := stripANSI(wiz.View())

	if strings.Count(locationView, recommendedTag) != 1 || !strings.Contains(locationView, "Falkenstein, Germany") {
		t.Fatalf("location view:\n%s", locationView)
	}

	wiz.step = stepMachine
	machineView := stripANSI(wiz.View())

	if strings.Count(machineView, recommendedTag) != 1 || strings.Contains(machineView, "recommended") {
		t.Fatalf("machine view:\n%s", machineView)
	}
}

func TestCodingToolStepTogglesTheRecommendedTool(t *testing.T) {
	t.Parallel()

	wiz := New(config.Config{AITools: config.ToolOpenCode}, testDest, false, adminTailnet(), nil)
	wiz.step = stepTools
	wiz.width = 88
	wiz.height = 32
	wiz.toolChoice = config.ToolOpenCode

	view := stripANSI(wiz.View())
	assertViewContains(
		t,
		view,
		"Coding tools",
		"beside Git and Docker",
		toolChecked+" OpenCode  "+recommendedTag,
		toolUnchecked+" Claude Code",
		toolUnchecked+" Grok Build",
		toolUnchecked+" Codex",
	)

	openAt := strings.Index(view, "OpenCode")
	claudeAt := strings.Index(view, "Claude Code")
	grokAt := strings.Index(view, "Grok Build")
	codexAt := strings.Index(view, "Codex")

	if openAt > claudeAt || claudeAt > grokAt || grokAt > codexAt {
		t.Fatalf("tool order:\n%s", view)
	}

	// A row that ends in a state word is a switch, not a checkbox.
	for line := range strings.SplitSeq(view, "\n") {
		row := strings.TrimRight(line, " ")
		if strings.HasSuffix(row, " on") || strings.HasSuffix(row, " off") {
			t.Fatalf("selection should be a checkbox:\n%s", view)
		}
	}

	if strings.Count(view, recommendedTag) != 1 {
		t.Fatalf("recommended count:\n%s", view)
	}

	model, _ := wiz.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})

	next, ok := model.(Model)
	if !ok {
		t.Fatal("model")
	}

	if next.tools != config.ToolNone {
		t.Fatalf("tools %q", next.tools)
	}
}

// falkenstein is the seed location with the given type counts.
func falkenstein(available, unavailable int) hetzner.LocationOffer {
	return hetzner.LocationOffer{
		Name:        config.SeedLocation,
		City:        "Falkenstein",
		Country:     "DE",
		NetworkZone: zoneEUCentral,
		Latitude:    50.4761,
		Longitude:   12.3701,
		Available:   available,
		Unavailable: unavailable,
	}
}

// machine is a shared x86 type offered only at the seed location.
func machine(name string, creatable, recommended bool, hourly, monthly string) hetzner.MachineOffer {
	return hetzner.MachineOffer{
		Name:     name,
		Category: "cost_optimized",
		CPU:      "shared",
		Arch:     "x86",
		Storage:  "local",
		Cores:    2,
		Memory:   4,
		Disk:     40,
		Places: map[string]hetzner.PlaceOffer{config.SeedLocation: {
			Creatable:    creatable,
			Recommended:  recommended,
			HourlyGross:  hourly,
			MonthlyGross: monthly,
			Included:     includedTraffic,
		}},
	}
}

func assertViewContains(t *testing.T, view string, wants ...string) {
	t.Helper()

	for _, want := range wants {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

// ansiPattern matches color codes and hyperlink codes.
var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]|\x1b\]8;[^\x07]*\x07`)

func stripANSI(text string) string {
	return ansiPattern.ReplaceAllString(text, "")
}
