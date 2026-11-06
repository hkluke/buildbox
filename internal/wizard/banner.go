package wizard

import (
	"fmt"
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/hkluke/buildbox/internal/console"
)

// The banner is the logo drawn in half-block characters: a cell holds two
// pixels, one above the other, so pixels come out square.

const (
	bannerTagline = "Let your agent think inside the box."

	// The cube's loose pieces leave its bitten corner up and to the right,
	// in pairs, and fade as they go. They move one pixel every driftTicks
	// spinner ticks; after pieceGap moves each piece stands where the next
	// one stood, so the drift loops.
	driftTicks  = 2
	driftWeight = 2
	pieceGap    = 3
	pieceSize   = 2
	pieceRows   = 3
	pieceFadeAt = 6
	cornerX     = 21
	cornerY     = 6

	// The loading screen stays up for 3s, at the spinner's ten ticks a
	// second, so it never just flashes by. init runs rarely, so the wait is
	// cheap. A slower load is not held any longer, and a key skips the rest.
	minLoadTicks = 30

	// The canvas leaves room for the pieces above the cube and to its right,
	// and as much on the left, so the cube itself is centered.
	heroAbove  = 4
	heroBeside = 11

	// cubeSide is a row of the cube's straight sides, below the top face.
	cubeSide = "LLLLLLLLLLRRRRRRRRRR"

	wordmarkWidth = 33
	wordmarkRows  = 3

	// bannerMinHeight is the terminal height from which every screen still
	// fits with the wordmark in its header.
	bannerMinHeight = 29
	quitHint        = "ctrl+c quit"
	spinnerGap      = "  "
)

// cubeArt is the logo's cube with its corner missing, as T, L, and R for its
// top, left, and right faces.
func cubeArt() []string {
	return []string{
		".........TT.........",
		".......TTTTTT.......",
		".....TTTTTTTTTT.....",
		"...TTTTTTTTTTTTR....",
		".TTTTTTTTTTTTRRR....",
		"LTTTTTTTTTTTTRRR....",
		"LLLTTTTTTTTTTTTR....",
		"LLLLLTTTTTTTTTTR....",
		"LLLLLLLTTTTTTRRRT...",
		"LLLLLLLLLTTRRRRRTTT.",
		"LLLLLLLLLLRRRRRRTTTR",
		"LLLLLLLLLLRRRRRRTRRR",
		cubeSide,
		cubeSide,
		cubeSide,
		cubeSide,
		cubeSide,
		".LLLLLLLLLRRRRRRRRR.",
		"...LLLLLLLRRRRRRR...",
		".....LLLLLRRRRR.....",
		".......LLLRRR.......",
		".........LR.........",
	}
}

// wordArt is "buildbox" in the lowercase pixel font of the logo's wordmark.
func wordArt() []string {
	return []string{
		"#.........#.#....#.#.............",
		"#...........#....#.#.............",
		"####.#..#.#.#.####.####.####.#..#",
		"#..#.#..#.#.#.#..#.#..#.#..#..##.",
		"#..#.#..#.#.#.#..#.#..#.#..#..##.",
		"####.####.#.#.####.####.####.#..#",
	}
}

// The logo's teal, darker on a light background and lighter on a dark one.
func faceColor(face byte) lipgloss.TerminalColor {
	switch face {
	case 'T':
		return lipgloss.AdaptiveColor{Light: "#0F3B38", Dark: "#17574F"}
	case 'L':
		return lipgloss.AdaptiveColor{Light: "#167A6C", Dark: "#1C8C7A"}
	case 'R':
		return lipgloss.AdaptiveColor{Light: "#1F9E8A", Dark: "#2BB8A0"}
	default:
		return nil
	}
}

// pieceColor lightens a piece the farther it has drifted.
func pieceColor(step int) lipgloss.TerminalColor {
	shades := []lipgloss.AdaptiveColor{
		{Light: "#0F3B38", Dark: "#17574F"},
		{Light: "#1A8574", Dark: "#239E8A"},
		{Light: "#2FA592", Dark: "#3DBBA5"},
		{Light: "#5FC4B0", Dark: "#86D9C7"},
		{Light: "#8ED8C8", Dark: "#9FE2D3"},
		{Light: "#B5E6DB", Dark: "#C8F2E8"},
	}

	return shades[min(step, len(shades)-1)]
}

type shade [3]float64

// wordColor runs the wordmark from teal to mint, left to right.
func wordColor(col int) lipgloss.TerminalColor {
	at := float64(col) / float64(wordmarkWidth-1)

	return lipgloss.AdaptiveColor{
		Light: blend(shade{0x0F, 0x3B, 0x38}, shade{0x1F, 0x9E, 0x8A}, at),
		Dark:  blend(shade{0x2B, 0xB8, 0xA0}, shade{0xB4, 0xEC, 0xDF}, at),
	}
}

func blend(from, to shade, at float64) string {
	var mixed [3]int
	for i := range mixed {
		mixed[i] = int(math.Round(from[i] + (to[i]-from[i])*at))
	}

	return fmt.Sprintf("#%02X%02X%02X", mixed[0], mixed[1], mixed[2])
}

// pixels is a picture; nil is the terminal's own background.
type pixels [][]lipgloss.TerminalColor

func newPixels(width, height int) pixels {
	pic := make(pixels, height)
	for row := range pic {
		pic[row] = make([]lipgloss.TerminalColor, width)
	}

	return pic
}

func (pic pixels) set(col, row int, color lipgloss.TerminalColor) {
	if row >= 0 && row < len(pic) && col >= 0 && col < len(pic[row]) {
		pic[row][col] = color
	}
}

// lines draws two pixel rows per text line.
func (pic pixels) lines() []string {
	out := make([]string, 0, (len(pic)+1)/twoLines)

	for row := 0; row < len(pic); row += twoLines {
		var line strings.Builder

		for col, upper := range pic[row] {
			var lower lipgloss.TerminalColor
			if row+1 < len(pic) {
				lower = pic[row+1][col]
			}

			line.WriteString(halfBlock(upper, lower))
		}

		out = append(out, line.String())
	}

	return out
}

func halfBlock(upper, lower lipgloss.TerminalColor) string {
	switch {
	case upper == nil && lower == nil:
		return " "
	case lower == nil:
		return lipgloss.NewStyle().Foreground(upper).Render("▀")
	case upper == nil:
		return lipgloss.NewStyle().Foreground(lower).Render("▄")
	case upper == lower:
		return lipgloss.NewStyle().Foreground(upper).Render("█")
	default:
		return lipgloss.NewStyle().Foreground(upper).Background(lower).Render("▀")
	}
}

// wordmarkLines is the wordmark, three lines tall.
func wordmarkLines() []string {
	art := wordArt()
	pic := newPixels(wordmarkWidth, len(art))

	for row, line := range art {
		for col, mark := range line {
			if mark == '#' {
				pic.set(col, row, wordColor(col))
			}
		}
	}

	return pic.lines()
}

// cubeLines is the cube with its pieces drift steps along their way out.
func cubeLines(drift int) []string {
	art := cubeArt()
	width := len(art[0])
	pic := newPixels(heroBeside+width+heroBeside, heroAbove+len(art))

	for row, line := range art {
		for col := range line {
			pic.set(heroBeside+col, heroAbove+row, faceColor(line[col]))
		}
	}

	shift := (drift / driftTicks) % pieceGap

	for across := range pieceRows {
		for _, rise := range []int{across, across + 1} {
			// A piece's distance from the corner, in thirds of a gap: one
			// pixel up and one across is two thirds of the way to the next.
			far := pieceGap*(across+rise) + shift*driftWeight
			if far >= pieceFadeAt*pieceGap {
				continue
			}

			col := heroBeside + cornerX + pieceGap*across + shift
			row := heroAbove + cornerY - pieceGap*rise - shift
			fillPiece(pic, col, row, pieceColor(far/pieceGap))
		}
	}

	return pic.lines()
}

func fillPiece(pic pixels, col, row int, color lipgloss.TerminalColor) {
	for down := range pieceSize {
		for across := range pieceSize {
			pic.set(col+across, row+down, color)
		}
	}
}

// heroLines is the loading screen: the logo, the tagline, and what init is
// waiting for.
func (wiz Model) heroLines() []string {
	var lines []string

	if wiz.color {
		lines = append(lines, cubeLines(wiz.drift)...)
		lines = append(lines, "")
	}

	lines = append(lines, wordmarkLines()...)
	lines = append(lines, "", wiz.paint.Muted.Render(bannerTagline), "")

	wait := strings.Split(console.WrapText(wiz.loadingText(), wiz.innerWidth()-lipgloss.Width(spinnerGap)-1), "\n")
	for i, text := range wait {
		lead := strings.Repeat(" ", lipgloss.Width(wiz.spin.View())) + spinnerGap
		if i == 0 {
			lead = wiz.spin.View() + spinnerGap
		}

		lines = append(lines, lead+wiz.paint.Text.Render(text))
	}

	return append(lines, "", wiz.paint.Muted.Render(quitHint))
}

// heroFits is whether the loading screen has room for the logo.
func (wiz Model) heroFits() bool {
	heroWidth := heroBeside + len(cubeArt()[0]) + heroBeside

	return wiz.width >= heroWidth+twoLines*screenPadX && wiz.height > len(wiz.heroLines())
}

func (wiz Model) viewHero() string {
	block := lipgloss.JoinVertical(lipgloss.Center, wiz.heroLines()...)

	return lipgloss.Place(wiz.width, wiz.height, lipgloss.Center, lipgloss.Center, block)
}

// headerBanner is whether the header shows the wordmark. Every screen shows
// it once the terminal is bannerMinHeight tall, so the header stays the same
// from screen to screen, and the overview only while every row still fits.
func (wiz Model) headerBanner() bool {
	if wiz.height < bannerMinHeight {
		return false
	}

	if wiz.step != stepHub {
		return true
	}

	room := listWindow{budget: 0, width: wiz.innerWidth()}
	need := wiz.hubExtraLines()

	for _, row := range wiz.hubRows() {
		need += room.cost(row)
	}

	return wiz.height-hubChrome-(wordmarkRows-1) >= need
}

// hintOverflow is how many lines the hub's hint wraps to beyond the one
// hubChrome counts.
func (wiz Model) hintOverflow() int {
	_, hint := wiz.question()

	return max(len(wiz.hintLines(hint, wiz.innerWidth()))-1, 0)
}
