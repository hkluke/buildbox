package main

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/hkluke/buildbox/internal/abort"
	"github.com/hkluke/buildbox/internal/console"
	"github.com/hkluke/buildbox/internal/hetzner"
)

const (
	priceTypeSmall = "cx23"
	priceTypeARM   = "cax21"
	priceLocFSN    = "fsn1"
	priceLocASH    = "ash"
	priceCurrency  = "EUR"
)

func TestParsePriceArgsReadsEveryFlagBothWays(t *testing.T) {
	t.Parallel()

	query, err := parsePriceArgs([]string{
		flagProvider, providerHetzner, "--per=" + perHour,
		flagCores, "4", "--memory=8", flagDisk, "80", "--arch", archARM, "--cpu=" + cpuShared,
		"--location", priceLocFSN + "," + priceLocASH, "--location= " + priceLocASH + ", nbg1", "--cheapest", "--json",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}

	want := priceQuery{
		provider: defaultProvider(),
		per:      perHour,
		filter: hetzner.Filter{
			Cores: cores(4, 4), Memory: memory(8, 8), Disk: disks(80, 80), Arch: archARM, CPU: cpuShared,
			Locations: []string{priceLocFSN, priceLocASH, "nbg1"},
		},
		cheapest: true,
		asJSON:   true,
	}
	if !reflect.DeepEqual(query, want) {
		t.Fatalf("query = %+v", query)
	}
}

func TestParsePriceArgsReadsTheMinimumFormsAndRefusesBoth(t *testing.T) {
	t.Parallel()

	query, err := parsePriceArgs([]string{
		"--min-cores", "2", "--max-cores=8", flagMinMemory + "=8", flagMaxMemory, "32",
		flagMinDisk, "100", "--max-disk", "100",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}

	want := hetzner.Filter{MinCores: 2, MaxCores: 8, MinMemory: 8, MaxMemory: 32, MinDisk: 100, MaxDisk: 100}
	if !reflect.DeepEqual(query.filter, want) {
		t.Fatalf("filter = %+v", query.filter)
	}

	for _, args := range [][]string{
		{flagCores, "2", "--min-cores", "4"},
		{flagCores, "2", "--max-cores", "4"},
		{flagMinMemory, "8", flagMemory + "=16"},
		{flagDisk, "80", flagMinDisk, "80"},
		{flagMinMemory, "16", flagMaxMemory, "8"},
	} {
		_, err := parsePriceArgs(args)
		if err == nil {
			t.Fatalf("%v: no error", args)
		}
	}
}

func TestParsePriceArgsTakesSeveralExactSizes(t *testing.T) {
	t.Parallel()

	query, err := parsePriceArgs([]string{flagMemory, "16,8", flagMemory + "=32", flagMemory, "8", "--cores=2, 4"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}

	want := hetzner.Filter{Cores: cores(2, 2, 4, 4), Memory: memory(8, 8, 16, 16, 32, 32)}
	if !reflect.DeepEqual(query.filter, want) {
		t.Fatalf("filter = %+v", query.filter)
	}

	for _, args := range [][]string{
		{flagMemory, "8,big"},
		{flagMemory, ","},
		{flagMinMemory, "8,16"},
		{"--max-disk=80,160"},
	} {
		_, err := parsePriceArgs(args)
		if err == nil {
			t.Fatalf("%v: no error", args)
		}
	}
}

// Ranges are closed, a unit on the right applies to a bare left, and they
// list alongside exact sizes.
func TestParsePriceArgsReadsRanges(t *testing.T) {
	t.Parallel()

	query, err := parsePriceArgs([]string{
		flagMemory, "8-16,32", flagMemory, "1-2TB", flagCores, "2-4", flagDisk, "80G-1TB",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}

	want := hetzner.Filter{Cores: cores(2, 4), Memory: memory(8, 16, 32, 32, 1000, 2000), Disk: disks(80, 1000)}
	if !reflect.DeepEqual(query.filter, want) {
		t.Fatalf("filter = %+v", query.filter)
	}

	for _, args := range [][]string{
		{flagMemory, "16-8"},
		{flagMemory, "8-"},
		{flagMemory, "-16"},
		{flagMemory, "8-16-32"},
		{flagMemory, "8–16"},
		{flagMinMemory, "8-16"},
		{flagDisk, "1-1.5G"},
	} {
		_, err := parsePriceArgs(args)
		if err == nil {
			t.Fatalf("%v: no error", args)
		}
	}
}

// cores, memory, and disks build span lists from low, high pairs.
func cores(pairs ...int) []hetzner.Span[int] {
	spans := make([]hetzner.Span[int], 0, len(pairs)/2)
	for index := 0; index+1 < len(pairs); index += 2 {
		spans = append(spans, hetzner.Span[int]{Low: pairs[index], High: pairs[index+1]})
	}

	return spans
}

func disks(pairs ...int) []hetzner.Span[int] {
	return cores(pairs...)
}

func memory(pairs ...float32) []hetzner.Span[float32] {
	spans := make([]hetzner.Span[float32], 0, len(pairs)/2)
	for index := 0; index+1 < len(pairs); index += 2 {
		spans = append(spans, hetzner.Span[float32]{Low: pairs[index], High: pairs[index+1]})
	}

	return spans
}

func TestParsePriceArgsReadsUnitsOnMemoryAndDisk(t *testing.T) {
	t.Parallel()

	exact, err := parsePriceArgs([]string{flagMemory, "8GB,16g", flagDisk, "960 gb"})
	if err != nil {
		t.Fatalf("exact err = %v", err)
	}

	wantExact := hetzner.Filter{Memory: memory(8, 8, 16, 16), Disk: disks(960, 960)}
	if !reflect.DeepEqual(exact.filter, wantExact) {
		t.Fatalf("exact filter = %+v", exact.filter)
	}

	bounds, err := parsePriceArgs([]string{flagMaxMemory, "1.5T", flagMinDisk, "500G", "--max-disk=1TB"})
	if err != nil {
		t.Fatalf("bounds err = %v", err)
	}

	if want := (hetzner.Filter{MaxMemory: 1500, MinDisk: 500, MaxDisk: 1000}); !reflect.DeepEqual(bounds.filter, want) {
		t.Fatalf("bounds filter = %+v", bounds.filter)
	}

	for _, args := range [][]string{
		{flagCores, "4G"},
		{flagMemory, "8GiB"},
		{flagMemory, "8M"},
		{flagMemory, "0.5"},
		{flagDisk, "1.5G"},
		{"--max-disk", "TB"},
	} {
		_, err := parsePriceArgs(args)
		if err == nil {
			t.Fatalf("%v: no error", args)
		}
	}
}

func TestNoMatchNotePointsAtTheMinimumForm(t *testing.T) {
	t.Parallel()

	headline, hint := noMatchNote(hetzner.Filter{Disk: disks(100, 100)})
	if headline != "No server type has exactly 100 GB of disk" || hint != "--min-disk 100 includes larger ones." {
		t.Fatalf("one exact size = %q / %q", headline, hint)
	}

	headline, hint = noMatchNote(hetzner.Filter{Memory: memory(6, 6, 12, 12, 24, 24)})
	if headline != "No server type has exactly 6 GB, 12 GB, or 24 GB of memory" ||
		hint != "--min-memory 6 includes larger ones." {
		t.Fatalf("several exact sizes = %q / %q", headline, hint)
	}

	headline, hint = noMatchNote(hetzner.Filter{Memory: memory(6, 7, 12, 12)})
	if headline != "No server type has 6 GB to 7 GB or 12 GB of memory" || !strings.Contains(hint, "Widen the range") {
		t.Fatalf("range = %q / %q", headline, hint)
	}

	headline, hint = noMatchNote(hetzner.Filter{Memory: memory(8, 8), Cores: cores(4, 4)})
	if headline != "No server type matches" || !strings.Contains(hint, "Widen the filter") {
		t.Fatalf("two exact sizes = %q / %q", headline, hint)
	}

	headline, _ = noMatchNote(hetzner.Filter{MinMemory: 999})
	if headline != "No server type matches" {
		t.Fatalf("minimum only = %q", headline)
	}
}

// Values match regardless of case, since the table prints locations in
// capitals, and are stored the way the output prints them.
func TestParsePriceArgsIgnoresCaseInValues(t *testing.T) {
	t.Parallel()

	query, err := parsePriceArgs([]string{
		flagProvider, "Hetzner", "--per", "Hour", flagArch, "ARM", "--cpu", "Dedicated", flagLocation, "ASH, Nbg1",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}

	want := priceQuery{
		provider: defaultProvider(),
		per:      perHour,
		filter:   hetzner.Filter{Arch: archARM, CPU: cpuDedicated, Locations: []string{priceLocASH, "nbg1"}},
	}
	if !reflect.DeepEqual(query, want) {
		t.Fatalf("query = %+v", query)
	}
}

// The location hint names the provider and lists its catalog's locations,
// so a second provider needs no new wording.
func TestCheckLocationsNamesTheProviderAndItsLocations(t *testing.T) {
	t.Parallel()

	_, shown := priceFixture()
	cat := hetzner.Catalog{Locations: shown}

	err := checkLocations(defaultProvider(), cat, []string{priceLocASH})
	if err != nil {
		t.Fatalf("known location: %v", err)
	}

	var failed *abort.Error

	err = checkLocations(defaultProvider(), cat, []string{"mars"})
	if !errors.As(err, &failed) || failed.Headline != "Unknown location: mars" ||
		failed.Hint != "Locations on Hetzner: fsn1 and ash." {
		t.Fatalf("err = %#v", err)
	}
}

func TestParsePriceArgsDefaultsToHetznerPerMonth(t *testing.T) {
	t.Parallel()

	plain, err := parsePriceArgs(nil)
	if err != nil {
		t.Fatalf("err = %v", err)
	}

	want := priceQuery{provider: defaultProvider(), per: perMonth}
	if !reflect.DeepEqual(plain, want) {
		t.Fatalf("plain = %+v", plain)
	}
}

func TestParsePriceArgsSaysThereIsOnlyOneProvider(t *testing.T) {
	t.Parallel()

	var failed *abort.Error

	_, err := parsePriceArgs([]string{flagProvider, "aws"})
	if !errors.As(err, &failed) || failed.Headline != "No provider called aws" ||
		!strings.Contains(failed.Hint, "Only hetzner is available for now") {
		t.Fatalf("err = %#v", err)
	}
}

func TestParsePriceArgsRejectsBadValues(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{flagProvider, "aws"},
		{"--per", "week"},
		{flagCores, "four"},
		{flagCores, "0"},
		{flagMemory},
		{"--disk="},
		{"--arch", "sparc"},
		{"--cpu=fast"},
		{"--nope"},
		{priceLocFSN},
		{"--location", ","},
		{"--location="},
	} {
		_, err := parsePriceArgs(args)
		if err == nil {
			t.Fatalf("%v: no error", args)
		}
	}
}

// priceFixture is two types in two locations: one everywhere, one sold out
// in Falkenstein and not offered in Ashburn.
func priceFixture() ([]hetzner.MachineOffer, []hetzner.LocationOffer) {
	shown := []hetzner.LocationOffer{{Name: priceLocFSN}, {Name: priceLocASH}}
	rows := []hetzner.MachineOffer{
		{
			Name: priceTypeSmall, CPU: cpuShared, Arch: archX86, Storage: "local", Cores: 2, Memory: 4, Disk: 40,
			Places: map[string]hetzner.PlaceOffer{
				priceLocFSN: {Creatable: true, HourlyGross: "0.0061000000", MonthlyGross: "3.7900000000"},
				priceLocASH: {Creatable: true, HourlyGross: "0.0080000000", MonthlyGross: "4.9900000000"},
			},
		},
		{
			Name: priceTypeARM, CPU: cpuShared, Arch: archARM, Storage: storageCeph, Cores: 4, Memory: 8, Disk: 80,
			Places: map[string]hetzner.PlaceOffer{
				priceLocFSN: {HourlyGross: "0.0104000000", MonthlyGross: "6.4900000000"},
			},
		},
	}

	return rows, shown
}

func TestPriceTableShowsTheMonthlyCapAndMarksTheConfig(t *testing.T) {
	t.Parallel()

	rows, shown := priceFixture()

	var buf bytes.Buffer

	mark := priceMark{serverType: priceTypeSmall, location: priceLocFSN}
	view := priceView{provider: defaultProvider(), currency: priceCurrency, per: perMonth, mark: mark}
	printPriceTable(console.NewWriter(&buf), view, rows, shown)

	want := "Monthly cap, gross. Hetzner bills by the hour and never more than this per month.\n" +
		"\n" +
		"TYPE   CPU           MEMORY  DISK       FSN1    ASH\n" +
		"cx23   2 shared x86  4 GB    40 GB     €3.79  €4.99\n" +
		"cax21  4 shared arm  8 GB    80 GB  sold out      -\n" +
		"\n" +
		"Your config: cx23 in fsn1\n"
	if buf.String() != want {
		t.Fatalf("table =\n%s\nwant\n%s", buf.String(), want)
	}
}

func TestPriceTableShowsTheHourlyRateOnRequest(t *testing.T) {
	t.Parallel()

	rows, shown := priceFixture()

	var buf bytes.Buffer

	view := priceView{provider: defaultProvider(), currency: priceCurrency, per: perHour}
	printPriceTable(console.NewWriter(&buf), view, rows, shown)

	want := "Hourly rate, gross, as Hetzner bills. A month is capped at the monthly price.\n" +
		"\n" +
		"TYPE   CPU           MEMORY  DISK       FSN1      ASH\n" +
		"cx23   2 shared x86  4 GB    40 GB   €0.0061  €0.0080\n" +
		"cax21  4 shared arm  8 GB    80 GB  sold out        -\n"
	if buf.String() != want {
		t.Fatalf("table =\n%s\nwant\n%s", buf.String(), want)
	}
}

func TestPriceRecordsCarryBothPricesWithoutASymbol(t *testing.T) {
	t.Parallel()

	rows, shown := priceFixture()
	records := priceRecords(providerHetzner, priceCurrency, rows, shown)

	if records[0].Provider != providerHetzner || records[0].Type != priceTypeSmall {
		t.Fatalf("record = %+v", records[0])
	}

	small := records[0].Prices[priceLocFSN]
	if small.Availability != hetzner.Available || small.Hourly == nil || small.Monthly == nil {
		t.Fatalf("small fsn1 = %+v", small)
	}

	if *small.Hourly != "0.0061" || *small.Monthly != "3.79" || small.Currency != priceCurrency {
		t.Fatalf("small fsn1 = %s %s %s", *small.Hourly, *small.Monthly, small.Currency)
	}
}

func TestCheapestLinesNameBothPricesAndEveryLocation(t *testing.T) {
	t.Parallel()

	rows, shown := priceFixture()
	small := rows[0]

	if got := specLine(small); got != "2 shared x86  4 GB  40 GB" {
		t.Fatalf("specs = %q", got)
	}

	got := cheapestPriceLine(priceCurrency, small.Places[priceLocFSN], shown)
	if got != "€0.0061 per hour, at most €3.79 per month, gross, in fsn1 and ash" {
		t.Fatalf("price line = %q", got)
	}

	record := cheapestRecordFor(providerHetzner, priceCurrency, small, small.Places[priceLocFSN], shown[:1])
	if record.Type != priceTypeSmall || record.Hourly == nil || record.Monthly == nil ||
		*record.Monthly != "3.79" || len(record.Locations) != 1 || record.Locations[0] != priceLocFSN {
		t.Fatalf("record = %+v", record)
	}
}

func TestPriceRecordsMarkSoldOutAndNotOffered(t *testing.T) {
	t.Parallel()

	rows, shown := priceFixture()
	records := priceRecords(providerHetzner, priceCurrency, rows, shown)

	arm := records[1]
	if arm.Storage != storageNetwork || arm.Prices[priceLocFSN].Availability != hetzner.SoldOut {
		t.Fatalf("arm = %+v", arm)
	}

	missing := arm.Prices[priceLocASH]
	if missing.Availability != hetzner.NotOffered || missing.Hourly != nil || missing.Monthly != nil {
		t.Fatalf("arm ash = %+v", missing)
	}

	if empty := priceRecords(providerHetzner, priceCurrency, nil, shown); len(empty) != 0 || empty == nil {
		t.Fatalf("empty = %#v", empty)
	}
}
