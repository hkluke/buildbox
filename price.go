package main

import (
	"cmp"
	"context"
	"encoding/json"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/hkluke/buildbox/internal/abort"
	"github.com/hkluke/buildbox/internal/config"
	"github.com/hkluke/buildbox/internal/console"
	"github.com/hkluke/buildbox/internal/hetzner"
)

const (
	flagProvider    = "--provider"
	flagPer         = "--per"
	flagCores       = "--cores"
	flagMinCores    = "--min-cores"
	flagMaxCores    = "--max-cores"
	flagMemory      = "--memory"
	flagMinMemory   = "--min-memory"
	flagMaxMemory   = "--max-memory"
	flagDisk        = "--disk"
	flagMinDisk     = "--min-disk"
	flagMaxDisk     = "--max-disk"
	flagArch        = "--arch"
	flagCPU         = "--cpu"
	flagLocation    = "--location"
	flagCheapest    = "--cheapest"
	providerHetzner = "hetzner"
	perHour         = "hour"
	perMonth        = "month"
	archX86         = "x86"
	archARM         = "arm"
	cpuShared       = "shared"
	cpuDedicated    = "dedicated"
	storageCeph     = "ceph"
	storageNetwork  = "network"
	soldOutCell     = "sold out"
	notOfferedCell  = "-"
	// specColumns is how many columns describe the type before the prices.
	specColumns = 4
	// gigabytesPerTerabyte is decimal, as Hetzner counts disk: 960 GB is
	// its largest, not 1024.
	gigabytesPerTerabyte = 1000.0
)

// providerInfo is what the output says about a provider: its name as
// --provider and --json spell it, its name as printed, and how it bills,
// worded for the caption above the table. The catalog says the rest. A
// second provider is one more entry in providers.
type providerInfo struct {
	name           string
	label          string
	monthlyCaption string
	hourlyCaption  string
}

func providers() []providerInfo {
	return []providerInfo{{
		name:           providerHetzner,
		label:          "Hetzner",
		monthlyCaption: "Monthly cap, gross. Hetzner bills by the hour and never more than this per month.",
		hourlyCaption:  "Hourly rate, gross, as Hetzner bills. A month is capped at the monthly price.",
	}}
}

// defaultProvider is the one price shows without --provider.
func defaultProvider() providerInfo {
	return providers()[0]
}

func providerByName(name string) (providerInfo, bool) {
	for _, info := range providers() {
		if info.name == name {
			return info, true
		}
	}

	return providerInfo{}, false
}

// caption says which figure the table shows and how the provider bills.
func (info providerInfo) caption(per string) string {
	if per == perHour {
		return info.hourlyCaption
	}

	return info.monthlyCaption
}

// priceQuery is what the arguments asked for.
type priceQuery struct {
	provider providerInfo
	per      string
	filter   hetzner.Filter
	cheapest bool
	asJSON   bool
}

// priceMark is the config's type and location, when there is a config.
type priceMark struct {
	serverType string
	location   string
}

// priceView is how the table shows the prices: whose they are, which
// figure, in what currency, and the config to mark.
type priceView struct {
	provider providerInfo
	currency string
	per      string
	mark     priceMark
}

// priceRecord is one element of price --json. Its keys are part of the
// interface scripts rely on, so they change only with a reason.
type priceRecord struct {
	Provider string                 `json:"provider"`
	Type     string                 `json:"type"`
	Cores    int                    `json:"cores"`
	Memory   float32                `json:"memory"`
	Disk     int                    `json:"disk"`
	Storage  string                 `json:"storage"`
	Arch     string                 `json:"arch"`
	CPU      string                 `json:"cpu"`
	Prices   map[string]placeRecord `json:"prices"`
}

// placeRecord is one location in a priceRecord. The prices are null where
// the type is not offered.
type placeRecord struct {
	Hourly       *string `json:"hourly"`
	Monthly      *string `json:"monthly"`
	Currency     string  `json:"currency"`
	Availability string  `json:"availability"`
}

// cheapestRecord is price --cheapest --json: one type, every location
// where it costs the least, and both prices. Its keys are stable too.
type cheapestRecord struct {
	Provider  string   `json:"provider"`
	Type      string   `json:"type"`
	Cores     int      `json:"cores"`
	Memory    float32  `json:"memory"`
	Disk      int      `json:"disk"`
	Storage   string   `json:"storage"`
	Arch      string   `json:"arch"`
	CPU       string   `json:"cpu"`
	Locations []string `json:"locations"`
	Hourly    *string  `json:"hourly"`
	Monthly   *string  `json:"monthly"`
	Currency  string   `json:"currency"`
}

// priceTable lays the rows out: the spec columns flush left, the prices
// flush right, like an invoice.
type priceTable struct {
	paint  console.Paint
	widths []int
	shown  []hetzner.LocationOffer
	mark   priceMark
}

func runPrice(ctx context.Context, args []string) error {
	query, err := parsePriceArgs(args)
	if err != nil {
		return err
	}

	client, err := newHetznerClient()
	if err != nil {
		return err
	}

	cat, err := hetzner.LoadPrices(ctx, client)
	if err != nil {
		return abort.Newf("Could not read %s's catalog.\n%s", query.provider.label, err.Error())
	}

	err = checkLocations(query.provider, cat, query.filter.Locations)
	if err != nil {
		return err
	}

	if query.cheapest {
		return printCheapest(query, cat)
	}

	return printPrices(query, cat)
}

func printPrices(query priceQuery, cat hetzner.Catalog) error {
	rows := cat.Offers(query.filter)
	shown := cat.Shown(query.filter)

	if query.asJSON {
		return printPriceJSON(query.provider.name, cat.Currency, rows, shown)
	}

	con := console.New()

	if len(rows) == 0 {
		headline, hint := noMatchNote(query.filter)
		con.Writeln(con.Paint.Muted.Render(headline + ". " + hint))

		return nil
	}

	view := priceView{provider: query.provider, currency: cat.Currency, per: query.per, mark: configMark()}
	printPriceTable(con, view, rows, shown)

	return nil
}

func parsePriceArgs(args []string) (priceQuery, error) {
	query := priceQuery{provider: defaultProvider(), per: perMonth}

	for len(args) > 0 {
		switch args[0] {
		case flagHelp, flagHelpShort:
			return priceQuery{}, showCommandHelp(cmdPrice)
		case flagJSON:
			query.asJSON = true
			args = args[1:]
		case flagCheapest:
			query.cheapest = true
			args = args[1:]
		default:
			rest, err := takePriceFlag(&query, args)
			if err != nil {
				return priceQuery{}, err
			}

			args = rest
		}
	}

	return query, sizeConflict(query.filter)
}

// sizeBounds is what the three flags of one size asked for: whether an
// exact size was given, the bounds, and the flag names an error would quote.
type sizeBounds struct {
	exact                       bool
	least, most                 float64
	exactFlag, minFlag, maxFlag string
}

// sizeConflict rejects an exact size next to a bound, since one would
// silently win, and a minimum above its maximum, which nothing could meet.
func sizeConflict(filter hetzner.Filter) error {
	sizes := []sizeBounds{
		{
			exact: len(filter.Cores) > 0, least: float64(filter.MinCores), most: float64(filter.MaxCores),
			exactFlag: flagCores, minFlag: flagMinCores, maxFlag: flagMaxCores,
		},
		{
			exact: len(filter.Memory) > 0, least: float64(filter.MinMemory), most: float64(filter.MaxMemory),
			exactFlag: flagMemory, minFlag: flagMinMemory, maxFlag: flagMaxMemory,
		},
		{
			exact: len(filter.Disk) > 0, least: float64(filter.MinDisk), most: float64(filter.MaxDisk),
			exactFlag: flagDisk, minFlag: flagMinDisk, maxFlag: flagMaxDisk,
		},
	}

	for _, size := range sizes {
		err := size.check()
		if err != nil {
			return err
		}
	}

	return nil
}

func (size sizeBounds) check() error {
	switch {
	case size.exact && (size.least != 0 || size.most != 0):
		return abort.Usage(size.exactFlag+" does not combine with "+size.minFlag+" or "+size.maxFlag, cmdPrice)
	case size.least != 0 && size.most != 0 && size.least > size.most:
		return abort.Usage(size.minFlag+" is more than "+size.maxFlag, cmdPrice)
	default:
		return nil
	}
}

// takePriceFlag reads one value flag, given as --flag VALUE or --flag=VALUE,
// and returns the arguments after it.
func takePriceFlag(query *priceQuery, args []string) ([]string, error) {
	name, value, joined := strings.Cut(args[0], "=")
	if !slices.Contains(priceValueFlags(), name) {
		return nil, abort.Usage("Unknown argument: "+args[0], cmdPrice)
	}

	if joined {
		return args[1:], setPriceFlag(query, name, value)
	}

	if len(args) == 1 {
		return nil, abort.Usage(name+" needs a value", cmdPrice)
	}

	return args[2:], setPriceFlag(query, name, args[1])
}

func priceValueFlags() []string {
	return []string{
		flagProvider, flagPer,
		flagCores, flagMinCores, flagMaxCores, flagMemory, flagMinMemory, flagMaxMemory, flagDisk, flagMinDisk, flagMaxDisk,
		flagArch, flagCPU, flagLocation,
	}
}

func setPriceFlag(query *priceQuery, name, value string) error {
	switch name {
	case flagProvider:
		return setProvider(query, value)
	case flagPer:
		return setChoice(&query.per, name, value, perHour, perMonth)
	case flagArch:
		return setChoice(&query.filter.Arch, name, value, archX86, archARM)
	case flagCPU:
		return setChoice(&query.filter.CPU, name, value, cpuShared, cpuDedicated)
	case flagLocation:
		return addLocations(&query.filter, value)
	default:
		return setSize(&query.filter, name, value)
	}
}

// addLocations reads one name or several separated by commas, and keeps
// each once. The columns stay in catalog order whatever the order typed.
func addLocations(filter *hetzner.Filter, value string) error {
	added := false

	for name := range strings.SplitSeq(value, ",") {
		// The table prints location names in capitals, so typing one back
		// that way has to work.
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}

		added = true

		if !slices.Contains(filter.Locations, name) {
			filter.Locations = append(filter.Locations, name)
		}
	}

	if !added {
		return abort.Usage(flagLocation+" needs a value", cmdPrice)
	}

	return nil
}

// setProvider says that there is only one provider, rather than that the
// value is wrong: a reader asking for another one wants to know it is not
// there yet.
func setProvider(query *priceQuery, value string) error {
	value = strings.ToLower(strings.TrimSpace(value))

	info, ok := providerByName(value)
	if !ok {
		names := make([]string, 0, len(providers()))
		for _, known := range providers() {
			names = append(names, known.name)
		}

		return abort.Hint(
			"No provider called "+value,
			"",
			"Only "+console.JoinAnd(names)+" is available for now, so "+flagProvider+" can be left out.",
		)
	}

	query.provider = info

	return nil
}

// sizeSpan is one parsed size, in cores or gigabytes: exact when the ends
// are equal, a range otherwise.
type sizeSpan struct {
	low  float64
	high float64
}

// setSize reads a size flag. An exact size takes one size or several
// separated by commas, each a number or a range, any of which matches; a
// bound takes one number.
func setSize(filter *hetzner.Filter, name, value string) error {
	store, ok := sizeFields(filter)[name]
	if !ok {
		return abort.Usage("Unknown argument: "+name, cmdPrice)
	}

	spans, err := sizeSpans(name, value)
	if err != nil {
		return err
	}

	for _, span := range spans {
		store(span)
	}

	return nil
}

func sizeSpans(name, value string) ([]sizeSpan, error) {
	spans := []sizeSpan{}

	for part := range strings.SplitSeq(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		span, ok := parseSizePart(name, part)
		if !ok {
			return nil, abort.Hint("Not a size: "+part, "", sizeHint(name))
		}

		spans = append(spans, span)
	}

	if len(spans) == 0 {
		return nil, abort.Usage(name+" needs a value", cmdPrice)
	}

	if !slices.Contains(exactSizeFlags(), name) && (len(spans) > 1 || spans[0].low != spans[0].high) {
		return nil, abort.Usage(name+" takes one number", cmdPrice)
	}

	return spans, nil
}

func exactSizeFlags() []string {
	return []string{flagCores, flagMemory, flagDisk}
}

func coreFlags() []string {
	return []string{flagCores, flagMinCores, flagMaxCores}
}

func diskFlags() []string {
	return []string{flagDisk, flagMinDisk, flagMaxDisk}
}

// parseSizePart reads one size or one range, as 8-16, for the flag called
// name. Only closed ranges count: an open end has its own --min- or --max-
// flag, and a token starting with a dash reads as a flag. A unit on the
// right end of a range applies to a bare left end, so 1-2TB is one to two
// terabytes.
func parseSizePart(name, part string) (sizeSpan, bool) {
	low, high, isRange := strings.Cut(part, "-")
	if !isRange {
		amount, _, ok := parseAmount(name, part, 1)

		return sizeSpan{low: amount, high: amount}, ok
	}

	if low == "" || high == "" || strings.Contains(high, "-") {
		return sizeSpan{}, false
	}

	top, factor, topOK := parseAmount(name, high, 1)
	if !topOK {
		return sizeSpan{}, false
	}

	bottom, _, bottomOK := parseAmount(name, low, factor)
	if !bottomOK || bottom > top {
		return sizeSpan{}, false
	}

	return sizeSpan{low: bottom, high: top}, true
}

// parseAmount reads one number for the flag called name and reports the
// unit factor it used. Cores take a whole number and no unit. Memory and
// disk take gigabytes, and disk must come to whole gigabytes, since no
// type has a fraction of one.
func parseAmount(name, text string, defaultFactor float64) (float64, float64, bool) {
	if slices.Contains(coreFlags(), name) {
		number, err := strconv.Atoi(strings.TrimSpace(text))

		return float64(number), 1, err == nil && number >= 1
	}

	gigabytes, factor, ok := parseGigabytes(text, defaultFactor)
	if !ok || gigabytes < 1 {
		return 0, 0, false
	}

	if slices.Contains(diskFlags(), name) && gigabytes != math.Trunc(gigabytes) {
		return 0, 0, false
	}

	return gigabytes, factor, true
}

// parseGigabytes reads a number of gigabytes, bare or with a G, GB, T, or
// TB suffix in either case, and reports the factor the unit meant; a bare
// number takes defaultFactor. A terabyte is a thousand gigabytes, the way
// Hetzner counts disk, so 1TB is 1000 and not 1024.
func parseGigabytes(text string, defaultFactor float64) (float64, float64, bool) {
	text = strings.ToUpper(strings.TrimSpace(text))
	factor := defaultFactor

	switch {
	case strings.HasSuffix(text, "TB"), strings.HasSuffix(text, "T"):
		factor = gigabytesPerTerabyte
		text = strings.TrimSuffix(strings.TrimSuffix(text, "B"), "T")
	case strings.HasSuffix(text, "GB"), strings.HasSuffix(text, "G"):
		factor = 1
		text = strings.TrimSuffix(strings.TrimSuffix(text, "B"), "G")
	}

	value, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
	if err != nil || math.IsInf(value, 0) || math.IsNaN(value) {
		return 0, 0, false
	}

	return value * factor, factor, true
}

func sizeHint(name string) string {
	if slices.Contains(coreFlags(), name) {
		return name + " takes a whole number of 1 or more, or a range as 2-4."
	}

	hint := name + " takes gigabytes, as 8 or 8GB, a range as 8-16, or terabytes as 1TB, which is 1000 GB."
	if slices.Contains(diskFlags(), name) {
		return hint + " Disk is whole gigabytes."
	}

	return hint
}

// sizeFields maps each size flag to the filter field it sets. An exact
// size or range is added to its list, once; a bound replaces the previous
// value.
func sizeFields(filter *hetzner.Filter) map[string]func(sizeSpan) {
	return map[string]func(sizeSpan){
		flagCores: func(span sizeSpan) {
			filter.Cores = addSpan(filter.Cores, hetzner.Span[int]{Low: int(span.low), High: int(span.high)})
		},
		flagMinCores: func(span sizeSpan) { filter.MinCores = int(span.low) },
		flagMaxCores: func(span sizeSpan) { filter.MaxCores = int(span.low) },
		flagMemory: func(span sizeSpan) {
			filter.Memory = addSpan(filter.Memory, hetzner.Span[float32]{Low: float32(span.low), High: float32(span.high)})
		},
		flagMinMemory: func(span sizeSpan) { filter.MinMemory = float32(span.low) },
		flagMaxMemory: func(span sizeSpan) { filter.MaxMemory = float32(span.low) },
		flagDisk: func(span sizeSpan) {
			filter.Disk = addSpan(filter.Disk, hetzner.Span[int]{Low: int(span.low), High: int(span.high)})
		},
		flagMinDisk: func(span sizeSpan) { filter.MinDisk = int(span.low) },
		flagMaxDisk: func(span sizeSpan) { filter.MaxDisk = int(span.low) },
	}
}

// addSpan keeps a list of spans sorted and without repeats, so the
// empty-result note can name them in order and start from the smallest.
func addSpan[Size int | float32](spans []hetzner.Span[Size], span hetzner.Span[Size]) []hetzner.Span[Size] {
	if slices.Contains(spans, span) {
		return spans
	}

	spans = append(spans, span)
	slices.SortFunc(spans, func(left, right hetzner.Span[Size]) int {
		return cmp.Or(cmp.Compare(left.Low, right.Low), cmp.Compare(left.High, right.High))
	})

	return spans
}

// sizeNote is one size the filter asked for, worded for the empty result:
// what was asked, and the way out of it.
type sizeNote struct {
	asked string
	hint  string
}

func sizeNotes(filter hetzner.Filter) []sizeNote {
	notes := []sizeNote{}

	if len(filter.Cores) > 0 {
		notes = append(notes, spanNote(filter.Cores, strconv.Itoa, " vCPUs", flagMinCores))
	}

	if len(filter.Memory) > 0 {
		notes = append(notes, spanNote(filter.Memory, hetzner.MemoryLabel, " of memory", flagMinMemory))
	}

	if len(filter.Disk) > 0 {
		notes = append(notes, spanNote(filter.Disk, strconv.Itoa, " GB of disk", flagMinDisk))
	}

	return notes
}

// spanNote words one size's spans as "exactly 8 GB", "8 GB or 16 GB", or
// "6 GB to 7 GB". For exact sizes the --min- form is the way out; for a
// range, widening it is.
func spanNote[Size int | float32](
	spans []hetzner.Span[Size],
	label func(Size) string,
	unit string,
	minFlag string,
) sizeNote {
	words := make([]string, 0, len(spans))
	ranged := false

	for _, span := range spans {
		if span.Low != span.High {
			ranged = true

			words = append(words, label(span.Low)+" to "+label(span.High))

			continue
		}

		words = append(words, label(span.Low))
	}

	asked := strings.Replace(console.JoinAnd(words), " and ", " or ", 1) + unit
	if ranged {
		return sizeNote{asked: asked, hint: "Widen the range, or run " + abort.Prog() + " " + cmdPrice + " for every type."}
	}

	return sizeNote{
		asked: "exactly " + asked,
		hint:  minFlag + " " + strconv.Itoa(int(spans[0].Low)) + " includes larger ones.",
	}
}

// noMatchNote explains an empty result as a headline and a hint. One size
// that nothing has is the usual cause, and the note says how to widen it.
func noMatchNote(filter hetzner.Filter) (string, string) {
	notes := sizeNotes(filter)
	if len(notes) == 1 {
		return "No server type has " + notes[0].asked, notes[0].hint
	}

	return "No server type matches", "Widen the filter, or run " + abort.Prog() + " " + cmdPrice + " for every type."
}

// setChoice matches a value regardless of case and stores the canonical
// lowercase form, which is what the output prints.
func setChoice(target *string, name, value string, choices ...string) error {
	value = strings.ToLower(strings.TrimSpace(value))
	if !slices.Contains(choices, value) {
		return abort.Usage(name+" must be "+strings.Join(choices, " or "), cmdPrice)
	}

	*target = value

	return nil
}

// checkLocations rejects a --location the provider does not have, naming
// the ones its catalog does.
func checkLocations(info providerInfo, cat hetzner.Catalog, names []string) error {
	for _, name := range names {
		if cat.HasLocation(name) {
			continue
		}

		known := make([]string, 0, len(cat.Locations))
		for _, location := range cat.Locations {
			known = append(known, location.Name)
		}

		return abort.Hint("Unknown location: "+name, "", "Locations on "+info.label+": "+console.JoinAnd(known)+".")
	}

	return nil
}

// configMark is the config's type and location, or empty without a config.
// price needs no config, so an unreadable one is simply not marked.
func configMark() priceMark {
	path, ok := config.File()
	if !ok {
		return priceMark{}
	}

	cfg, err := config.LoadSeed(path)
	if err != nil {
		return priceMark{}
	}

	return priceMark{serverType: cfg.ServerType, location: cfg.ServerLoc}
}

// printCheapest answers with one type. Nothing matching is an error here,
// unlike the empty table, since a script asking for the cheapest box needs
// to see that there is none.
func printCheapest(query priceQuery, cat hetzner.Catalog) error {
	machine, places := cat.Cheapest(query.filter)
	if len(places) == 0 {
		headline, hint := noMatchNote(query.filter)

		return abort.Hint(headline, "", hint)
	}

	place := machine.Places[places[0].Name]

	if query.asJSON {
		return printCheapestJSON(query.provider.name, cat.Currency, machine, place, places)
	}

	con := console.New()
	con.Writeln(con.Paint.Title.Render(machine.Name) + columnGap + con.Paint.Text.Render(specLine(machine)))
	con.Writeln(con.Paint.Muted.Render(cheapestPriceLine(cat.Currency, place, places)))

	return nil
}

func specLine(machine hetzner.MachineOffer) string {
	return strings.Join([]string{
		strconv.Itoa(machine.Cores) + " " + machine.CPU + " " + machine.Arch,
		hetzner.MemoryLabel(machine.Memory),
		strconv.Itoa(machine.Disk) + " GB",
	}, columnGap)
}

// cheapestPriceLine gives both figures, since one type is too little
// context for a caption: the hourly rate is what is billed, the monthly
// price is the cap.
func cheapestPriceLine(currency string, place hetzner.PlaceOffer, places []hetzner.LocationOffer) string {
	return hetzner.FormatMoney(currency, place.HourlyGross, hetzner.HourlyFrac) + " per hour, at most " +
		hetzner.FormatMoney(currency, place.MonthlyGross, hetzner.MonthlyFrac) + " per month, gross, in " +
		console.JoinAnd(locationNames(places))
}

func locationNames(places []hetzner.LocationOffer) []string {
	names := make([]string, 0, len(places))
	for _, location := range places {
		names = append(names, location.Name)
	}

	return names
}

func printCheapestJSON(
	provider, currency string,
	machine hetzner.MachineOffer,
	place hetzner.PlaceOffer,
	places []hetzner.LocationOffer,
) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")

	err := encoder.Encode(cheapestRecordFor(provider, currency, machine, place, places))
	if err != nil {
		return abort.Newf("Could not write the cheapest box as JSON.\n%s", err.Error())
	}

	return nil
}

func cheapestRecordFor(
	provider, currency string,
	machine hetzner.MachineOffer,
	place hetzner.PlaceOffer,
	places []hetzner.LocationOffer,
) cheapestRecord {
	return cheapestRecord{
		Provider:  provider,
		Type:      machine.Name,
		Cores:     machine.Cores,
		Memory:    machine.Memory,
		Disk:      machine.Disk,
		Storage:   storageKind(machine.Storage),
		Arch:      machine.Arch,
		CPU:       machine.CPU,
		Locations: locationNames(places),
		Hourly:    amountOrNil(place.HourlyGross, hetzner.HourlyFrac),
		Monthly:   amountOrNil(place.MonthlyGross, hetzner.MonthlyFrac),
		Currency:  currency,
	}
}

func printPriceTable(
	con *console.Console,
	view priceView,
	rows []hetzner.MachineOffer,
	shown []hetzner.LocationOffer,
) {
	con.Writeln(con.Paint.Muted.Render(view.provider.caption(view.per)))
	con.Blank()

	table := make([][]string, 0, 1+len(rows))
	table = append(table, priceHeader(shown))

	for _, machine := range rows {
		table = append(table, priceCells(view, machine, shown))
	}

	layout := priceTable{paint: con.Paint, widths: columnWidths(table), shown: shown, mark: view.mark}

	for index, cells := range table {
		con.Writeln(layout.row(cells, index == 0))
	}

	if view.mark.serverType != "" && view.mark.location != "" {
		con.Blank()
		con.Writeln(con.Paint.Muted.Render("Your config: " + view.mark.serverType + " in " + view.mark.location))
	}
}

func priceHeader(shown []hetzner.LocationOffer) []string {
	cells := make([]string, 0, specColumns+len(shown))
	cells = append(cells, "TYPE", "CPU", "MEMORY", "DISK")

	for _, location := range shown {
		cells = append(cells, strings.ToUpper(location.Name))
	}

	return cells
}

func priceCells(view priceView, machine hetzner.MachineOffer, shown []hetzner.LocationOffer) []string {
	cells := make([]string, 0, specColumns+len(shown))
	cells = append(cells, machine.Name)
	cells = append(cells, strings.Split(specLine(machine), columnGap)...)

	for _, location := range shown {
		cells = append(cells, priceCell(view, machine, location.Name))
	}

	return cells
}

func priceCell(view priceView, machine hetzner.MachineOffer, location string) string {
	place, ok := machine.Places[location]

	switch {
	case !ok:
		return notOfferedCell
	case !place.Creatable:
		return soldOutCell
	case view.per == perHour:
		return hetzner.FormatMoney(view.currency, place.HourlyGross, hetzner.HourlyFrac)
	default:
		return hetzner.FormatMoney(view.currency, place.MonthlyGross, hetzner.MonthlyFrac)
	}
}

func columnWidths(table [][]string) []int {
	widths := make([]int, len(table[0]))

	for _, cells := range table {
		for column, cell := range cells {
			widths[column] = max(widths[column], lipgloss.Width(cell))
		}
	}

	return widths
}

// row pads before it colors, so escape codes never count as width.
func (layout priceTable) row(cells []string, header bool) string {
	parts := make([]string, len(cells))

	for column, cell := range cells {
		text := console.PadRight(cell, layout.widths[column])
		if column >= specColumns {
			text = console.PadLeft(cell, layout.widths[column])
		}

		parts[column] = layout.style(column, cell, header).Render(text)
	}

	return strings.Join(parts, columnGap)
}

func (layout priceTable) style(column int, cell string, header bool) lipgloss.Style {
	switch {
	case layout.marked(column, cell, header):
		return layout.paint.Accent
	case header:
		return layout.paint.Muted
	case column == 0:
		return layout.paint.Title
	case cell == soldOutCell:
		return layout.paint.Warn
	case cell == notOfferedCell:
		return layout.paint.Faint
	default:
		return layout.paint.Text
	}
}

// marked is the config's type in the first column, or its location in the
// header.
func (layout priceTable) marked(column int, cell string, header bool) bool {
	if header {
		return column >= specColumns && layout.shown[column-specColumns].Name == layout.mark.location
	}

	return column == 0 && cell == layout.mark.serverType
}

// printPriceJSON prints [] rather than null when nothing matches, so a
// script can always iterate over the result.
func printPriceJSON(provider, currency string, rows []hetzner.MachineOffer, shown []hetzner.LocationOffer) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")

	err := encoder.Encode(priceRecords(provider, currency, rows, shown))
	if err != nil {
		return abort.Newf("Could not write the prices as JSON.\n%s", err.Error())
	}

	return nil
}

func priceRecords(provider, currency string, rows []hetzner.MachineOffer, shown []hetzner.LocationOffer) []priceRecord {
	records := make([]priceRecord, 0, len(rows))

	for _, machine := range rows {
		prices := make(map[string]placeRecord, len(shown))
		for _, location := range shown {
			prices[location.Name] = placeRecordFor(currency, machine, location.Name)
		}

		records = append(records, priceRecord{
			Provider: provider,
			Type:     machine.Name,
			Cores:    machine.Cores,
			Memory:   machine.Memory,
			Disk:     machine.Disk,
			Storage:  storageKind(machine.Storage),
			Arch:     machine.Arch,
			CPU:      machine.CPU,
			Prices:   prices,
		})
	}

	return records
}

func placeRecordFor(currency string, machine hetzner.MachineOffer, location string) placeRecord {
	record := placeRecord{Currency: currency, Availability: machine.Availability(location)}

	place, ok := machine.Places[location]
	if !ok {
		return record
	}

	record.Hourly = amountOrNil(place.HourlyGross, hetzner.HourlyFrac)
	record.Monthly = amountOrNil(place.MonthlyGross, hetzner.MonthlyFrac)

	return record
}

func amountOrNil(amount string, frac int) *string {
	text, ok := hetzner.FormatAmount(amount, frac)
	if !ok {
		return nil
	}

	return &text
}

// storageKind spells Hetzner's "ceph" as network storage, the way init does.
func storageKind(kind string) string {
	if kind == storageCeph {
		return storageNetwork
	}

	return kind
}
