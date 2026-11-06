package wizard

import (
	"cmp"
	_ "embed"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/hkluke/buildbox/internal/console"
	"github.com/hkluke/buildbox/internal/hetzner"
)

// The Location screen shows the world from 75°N to 56°S, with each location
// where the Hetzner API says it is. It is drawn in braille, two dots across
// and four down a cell, which makes the dots square, and only every other
// dot is set, a light texture the markers stand out from.

// landHex is the land: a line per degree of latitude from 75°N, each 90 hex
// digits for the 360 one-degree cells from 180°W, high bit first. It comes
// from Natural Earth's 1:110m land polygons, which are in the public domain;
// gen_land.go makes it.
//
//go:generate go run gen_land.go
//go:embed land.txt
var landHex string

const (
	mapNorth    = 75
	mapSpan     = 131
	halfCircle  = 180
	fullCircle  = 360
	dotsAcross  = 2
	dotsDown    = 4
	cellsPerHex = 4
	hexDigits   = "0123456789abcdef"
	brailleBase = '⠀'

	// The map is at most mapMaxWidth columns, shrinks to the rows the
	// screen has left, and is left out below mapMinRows, where it is too
	// small to read.
	mapMaxWidth = 100
	mapMinRows  = 9

	// A dot is land when most of its landSamples × landSamples points are.
	landSamples  = 3
	landMajority = 5
	sampleMiddle = 0.5

	// The land around the selected location takes the accent's brass, most
	// at the marker and none glowReach of the map's width away, in glowSteps
	// shades. A cell is twice as tall as it is wide.
	glowReach    = 0.11
	glowCurve    = 1.4
	glowStrength = 0.9
	glowSteps    = 8
	cellAspect   = 2

	legendWidth  = 13
	calloutLift  = 2
	cornerWidth  = 2
	markOpen     = "●"
	markSoldOut  = "○"
	markSelected = "◉"
	locationKeys = "↑↓ move  ·  ←→ west/east  ·  enter select  ·  esc back  ·  q quit"
)

// landColor is the land's own shade, between the background and the muted
// text, mixed with glow of the accent's brass.
func landColor(glow float64) lipgloss.TerminalColor {
	return lipgloss.AdaptiveColor{
		Light: blend(shade{0xB8, 0xB2, 0xAC}, shade{0x8A, 0x5A, 0x22}, glow),
		Dark:  blend(shade{0x5A, 0x54, 0x4F}, shade{0xE0, 0xB1, 0x5A}, glow),
	}
}

// landMap is land.txt, a line per degree of latitude.
type landMap []string

func readLand() landMap {
	return strings.Fields(landHex)
}

// at reports whether the one-degree cell holding lon, lat is land.
func (land landMap) at(lon, lat float64) bool {
	row := int(math.Floor(mapNorth - lat))
	col := int(math.Floor(lon + halfCircle))

	if row < 0 || row >= len(land) || col < 0 || col >= fullCircle {
		return false
	}

	value := strings.IndexByte(hexDigits, land[row][col/cellsPerHex])

	return value>>(cellsPerHex-1-col%cellsPerHex)&1 == 1
}

// covered reports whether most of the square of side size, west and south
// of lon, lat, is land.
func (land landMap) covered(lon, lat, size float64) bool {
	hits := 0

	for across := range landSamples {
		for down := range landSamples {
			east := (float64(across) + sampleMiddle) * size / landSamples
			south := (float64(down) + sampleMiddle) * size / landSamples

			if land.at(lon+east, lat-south) {
				hits++
			}
		}
	}

	return hits >= landMajority
}

// mapGrid is the map at one width: its cells, and the degrees one dot spans.
type mapGrid struct {
	cols    int
	rows    int
	degrees float64
}

func newMapGrid(cols int) mapGrid {
	degrees := fullCircle / float64(cols*dotsAcross)

	return mapGrid{cols: cols, rows: int(math.Ceil(mapSpan / (degrees * dotsDown))), degrees: degrees}
}

// fitMap is the widest map, up to mapMaxWidth, that fits in rows, and
// whether it is still tall enough to read.
func fitMap(width, rows int) (mapGrid, bool) {
	for cols := min(width, mapMaxWidth); cols > 0; cols-- {
		if grid := newMapGrid(cols); grid.rows <= rows {
			return grid, grid.rows >= mapMinRows
		}
	}

	return mapGrid{cols: 0, rows: 0, degrees: 0}, false
}

// dots is the braille character for one cell's land, every other dot set.
func (grid mapGrid) dots(land landMap, col, row int) rune {
	bits := [dotsDown][dotsAcross]rune{{0x01, 0x08}, {0x02, 0x10}, {0x04, 0x20}, {0x40, 0x80}}

	var mask rune

	for down := range dotsDown {
		for across := range dotsAcross {
			dotX, dotY := col*dotsAcross+across, row*dotsDown+down
			if (dotX+dotY)%2 == 1 {
				continue
			}

			lon := float64(dotX)*grid.degrees - halfCircle
			if land.covered(lon, mapNorth-float64(dotY)*grid.degrees, grid.degrees) {
				mask |= bits[down][across]
			}
		}
	}

	return mask
}

// glow is how much brass a cell takes, by its distance from the selected
// marker, in steps so that runs of cells share a shade.
func (grid mapGrid) glow(col, row int, focus mapPin) float64 {
	across := float64(col - focus.col)
	down := float64((row - focus.row) * cellAspect)

	near := 1 - math.Hypot(across, down)/(glowReach*float64(grid.cols))
	if near <= 0 {
		return 0
	}

	return math.Round(math.Pow(near, glowCurve)*glowStrength*glowSteps) / glowSteps
}

// mapPin is a location's marker.
type mapPin struct {
	col   int
	row   int
	offer hetzner.LocationOffer
}

// pins places the locations west to east. Two in one cell sit side by side,
// the eastern one on the right, as Nuremberg and Falkenstein do.
func (grid mapGrid) pins(offers []hetzner.LocationOffer) []mapPin {
	pins := make([]mapPin, 0, len(offers))
	taken := map[[2]int]bool{}

	for _, offer := range westToEast(offers) {
		col := int((offer.Longitude + halfCircle) / (grid.degrees * dotsAcross))
		row := int((mapNorth - offer.Latitude) / (grid.degrees * dotsDown))
		col, row = min(max(col, 0), grid.cols-1), min(max(row, 0), grid.rows-1)

		for taken[[2]int{col, row}] && col < grid.cols-1 {
			col++
		}

		taken[[2]int{col, row}] = true
		pins = append(pins, mapPin{col: col, row: row, offer: offer})
	}

	return pins
}

func westToEast(offers []hetzner.LocationOffer) []hetzner.LocationOffer {
	sorted := slices.Clone(offers)
	slices.SortStableFunc(sorted, func(left, right hetzner.LocationOffer) int {
		if order := cmp.Compare(left.Longitude, right.Longitude); order != 0 {
			return order
		}

		return strings.Compare(left.Name, right.Name)
	})

	return sorted
}

// nextLocation is the location step places east of current, or west for a
// negative step, round the world past either end.
func nextLocation(offers []hetzner.LocationOffer, current string, step int) string {
	sorted := westToEast(offers)
	if len(sorted) == 0 {
		return ""
	}

	index := slices.IndexFunc(sorted, func(offer hetzner.LocationOffer) bool { return offer.Name == current })
	if index < 0 {
		return sorted[0].Name
	}

	return sorted[((index+step)%len(sorted)+len(sorted))%len(sorted)].Name
}

// mapCell is one character of the map: land in a shade, which neighbouring
// cells share, or the start of a marker or label already painted, which
// covers the cells after it.
type mapCell struct {
	text    string
	shade   lipgloss.TerminalColor
	painted string
	covered bool
}

type mapCanvas [][]mapCell

// paint puts text, drawn in style, at col, row.
func (canvas mapCanvas) paint(col, row int, text string, style lipgloss.Style) {
	canvas.place(col, row, style.Render(text), lipgloss.Width(text))
}

// place puts painted text, width cells wide, at col, row.
func (canvas mapCanvas) place(col, row int, painted string, width int) {
	if row < 0 || row >= len(canvas) || col < 0 || col+width > len(canvas[row]) {
		return
	}

	canvas[row][col] = mapCell{text: "", shade: nil, painted: painted, covered: false}
	for next := 1; next < width; next++ {
		canvas[row][col+next] = mapCell{text: "", shade: nil, painted: "", covered: true}
	}
}

func (canvas mapCanvas) lines() []string {
	lines := make([]string, 0, len(canvas))

	for _, row := range canvas {
		var (
			line, run strings.Builder
			shade     lipgloss.TerminalColor
		)

		flush := func() {
			if shade == nil {
				line.WriteString(run.String())
			} else if run.Len() > 0 {
				line.WriteString(lipgloss.NewStyle().Foreground(shade).Render(run.String()))
			}

			run.Reset()
		}

		for _, cell := range row {
			switch {
			case cell.covered:
			case cell.painted != "":
				flush()
				line.WriteString(cell.painted)
			default:
				if cell.shade != shade {
					flush()

					shade = cell.shade
				}

				run.WriteString(cell.text)
			}
		}

		flush()

		lines = append(lines, line.String())
	}

	return lines
}

// drawMap is the map with every location's marker, the selected one
// labelled, and a legend in the empty South Pacific.
func (wiz Model) drawMap(grid mapGrid) []string {
	pins := grid.pins(wiz.cat.Locations)

	focus := mapPin{col: -grid.cols, row: -grid.rows, offer: hetzner.LocationOffer{}}
	for _, pin := range pins {
		if pin.offer.Name == wiz.location {
			focus = pin
		}
	}

	land := readLand()
	canvas := make(mapCanvas, grid.rows)

	for row := range canvas {
		canvas[row] = make([]mapCell, grid.cols)

		for col := range canvas[row] {
			canvas[row][col] = mapCell{text: " ", shade: nil, painted: "", covered: false}

			if mask := grid.dots(land, col, row); mask != 0 {
				canvas[row][col].text = string(brailleBase + mask)
				canvas[row][col].shade = landColor(grid.glow(col, row, focus))
			}
		}
	}

	legend := wiz.drawLegend(canvas)

	for _, pin := range pins {
		mark, style := wiz.pinMark(pin.offer)
		canvas.paint(pin.col, pin.row, mark, style)
	}

	if focus.offer.Name != "" {
		wiz.callout(canvas, focus, pins, legend)
		canvas.paint(focus.col, focus.row, markSelected, wiz.paint.Accent)
	}

	return canvas.lines()
}

func (wiz Model) pinMark(offer hetzner.LocationOffer) (string, lipgloss.Style) {
	if offer.Available == 0 {
		return markSoldOut, wiz.paint.Faint
	}

	return markOpen, wiz.paint.OK.Bold(true)
}

// drawLegend explains the markers in the map's bottom left corner, and
// returns how many rows it takes.
func (wiz Model) drawLegend(canvas mapCanvas) int {
	type entry struct {
		mark  string
		label string
		style lipgloss.Style
	}

	entries := []entry{{markOpen, "available", wiz.paint.OK.Bold(true)}}
	if slices.ContainsFunc(wiz.cat.Locations, func(offer hetzner.LocationOffer) bool { return offer.Available == 0 }) {
		entries = append(entries, entry{markSoldOut, "sold out", wiz.paint.Faint})
	}

	entries = append(entries, entry{markSelected, "selected", wiz.paint.Accent})

	top := len(canvas) - len(entries)
	for index, item := range entries {
		gap := strings.Repeat(" ", max(legendWidth-lipgloss.Width(item.mark+" "+item.label), 0))
		painted := item.style.Render(item.mark) + wiz.paint.Muted.Render(" "+item.label) + gap
		canvas.place(0, top+index, painted, legendWidth)
	}

	return len(entries)
}

// calloutSide is where the label goes: above or below the marker, and
// running right or left from it.
type calloutSide struct {
	up     bool
	right  bool
	corner string
}

// spot is where a label span cells wide goes on this side: its row, its
// stem's row, and its first column.
func (side calloutSide) spot(focus mapPin, span int) (int, int, int) {
	labelRow, stemRow := focus.row-calloutLift, focus.row-1
	if !side.up {
		labelRow, stemRow = focus.row+calloutLift, focus.row+1
	}

	if side.right {
		return labelRow, stemRow, focus.col
	}

	return labelRow, stemRow, focus.col - span + 1
}

// callout labels the selected location on the first side that stays on
// the map and covers neither another marker nor the legend.
func (wiz Model) callout(canvas mapCanvas, focus mapPin, pins []mapPin, legend int) {
	text := " " + focus.offer.Name + " · " + locationLabel(focus.offer) + " "
	span := cornerWidth + lipgloss.Width(text)
	rows, cols := len(canvas), len(canvas[0])

	sides := []calloutSide{{true, true, "╭─"}, {true, false, "─╮"}, {false, true, "╰─"}, {false, false, "─╯"}}
	for _, side := range sides {
		labelRow, stemRow, start := side.spot(focus, span)

		inside := labelRow >= 0 && labelRow < rows && start >= 0 && start+span <= cols
		pastLegend := labelRow < rows-legend || start >= legendWidth

		if !inside || !pastLegend || calloutHides(focus, pins, labelRow, stemRow, start, span) {
			continue
		}

		if side.right {
			canvas.paint(start, labelRow, side.corner, wiz.paint.Code)
			canvas.paint(start+cornerWidth, labelRow, text, wiz.paint.Title)
		} else {
			canvas.paint(start, labelRow, text, wiz.paint.Title)
			canvas.paint(focus.col-1, labelRow, side.corner, wiz.paint.Code)
		}

		canvas.paint(focus.col, stemRow, "│", wiz.paint.Code)

		return
	}
}

// calloutHides reports whether a label from start, span cells wide, or its
// stem would cover another marker.
func calloutHides(focus mapPin, pins []mapPin, labelRow, stemRow, start, span int) bool {
	for _, pin := range pins {
		if pin.offer.Name == focus.offer.Name {
			continue
		}

		if pin.row == labelRow && pin.col >= start && pin.col < start+span {
			return true
		}

		if pin.row == stemRow && pin.col == focus.col {
			return true
		}
	}

	return false
}

// viewLocation shows the locations on the world map when the screen has
// room for one, and as the plain list otherwise.
func (wiz Model) viewLocation() string {
	title, hint := wiz.question()
	header := wiz.viewHeader(title, hint)

	list := wiz.locationLines()
	below := make([]string, 0, len(list)+headerLines)
	below = append(below, "")
	below = append(below, list...)
	below = append(below, "", wiz.viewFooter())

	room := wiz.height - twoLines*screenPadY - lineCount(header) - lineCount(strings.Join(below, "\n"))

	grid, fits := fitMap(wiz.innerWidth(), room)
	if !fits || len(wiz.cat.Locations) == 0 {
		return wiz.viewPick()
	}

	drawn := wiz.drawMap(grid)
	parts := make([]string, 0, len(drawn)+len(below)+1)
	parts = append(parts, header)
	parts = append(parts, drawn...)
	parts = append(parts, below...)

	return wiz.screen(strings.Join(parts, "\n"))
}

func lineCount(text string) int {
	return strings.Count(text, "\n") + 1
}

// locationColumns are the widths of the list under the map. A narrow screen
// drops the network zone, then the recommendation, then cuts the city.
type locationColumns struct {
	name  int
	place int
	zone  int
	state int
	tag   int
}

func (wiz Model) locationColumns() locationColumns {
	cols := locationColumns{name: 0, place: 0, zone: 0, state: 0, tag: 0}

	for _, offer := range wiz.cat.Locations {
		cols.name = max(cols.name, lipgloss.Width(offer.Name)+twoLines)
		cols.place = max(cols.place, lipgloss.Width(locationLabel(offer))+cornerWidth+1)
		cols.zone = max(cols.zone, lipgloss.Width(offer.NetworkZone)+cornerWidth+1)
		cols.state = max(cols.state, lipgloss.Width(locationState(offer))+cornerWidth)

		if offer.Name == wiz.suggestLocation {
			cols.tag = lipgloss.Width(recommendedTag) + cornerWidth + 1
		}
	}

	width := wiz.innerWidth() - cornerWidth
	if cols.name+cols.place+cols.zone+cols.state+cols.tag > width {
		cols.zone = 0
	}

	if cols.name+cols.place+cols.state+cols.tag > width {
		cols.tag = 0
	}

	cols.place = max(min(cols.place, width-cols.name-cols.zone-cols.state-cols.tag), 1)

	return cols
}

func locationState(offer hetzner.LocationOffer) string {
	if offer.Available == 0 {
		return "sold out"
	}

	return fmt.Sprintf("%d of %d types", offer.Available, offer.Available+offer.Unavailable)
}

// locationLines are the locations one to a line, for under the map.
func (wiz Model) locationLines() []string {
	cols := wiz.locationColumns()
	lines := make([]string, 0, len(wiz.cat.Locations))

	for _, offer := range wiz.cat.Locations {
		row := pickerRow{id: offer.Name, dim: offer.Available == 0}

		mark := "  "
		if offer.Name == wiz.location {
			mark = "▸ "
		}

		main := mark + padTo(offer.Name, cols.name) +
			padTo(console.TrimWidth(locationLabel(offer), cols.place-cornerWidth-1), cols.place)

		detail := wiz.paint.Muted
		if row.dim && offer.Name != wiz.location {
			detail = wiz.paint.Faint
		}

		glyph, glyphStyle := wiz.pinMark(offer)
		line := wiz.rowStyle(row).Render(main) + detail.Render(padTo(offer.NetworkZone, cols.zone)) +
			glyphStyle.Render(glyph) + detail.Render(" "+locationState(offer))

		if cols.tag > 0 && offer.Name == wiz.suggestLocation {
			line += wiz.paint.Muted.Render(strings.Repeat(" ", cornerWidth+1) + recommendedTag)
		}

		lines = append(lines, line)
	}

	return lines
}

// padTo pads text with spaces to width, or leaves it out when width is 0.
func padTo(text string, width int) string {
	if width <= 0 {
		return ""
	}

	return text + strings.Repeat(" ", max(width-lipgloss.Width(text), 0))
}
