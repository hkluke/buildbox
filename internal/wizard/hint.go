package wizard

import (
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/hkluke/buildbox/internal/console"
)

// A hint may name a button in curly quotes, “Like this”, which shows it in
// bold; straight quotes stay plain, since the policy lines use them. Its
// links become terminal hyperlinks, clickable even when cut short or
// wrapped, in terminals that support them; the others show plain text.
const (
	quoteOpen  = '“'
	quoteClose = '”'
	linkPrefix = "https://"
	linkTrail  = ".,:;)…"
	stepMarker = "1. "
)

type pieceKind int

const (
	piecePlain pieceKind = iota
	pieceLabel
	pieceLink
)

// hintPiece is a run of a hint line drawn one way. A link's target is the
// full URL, which the shown text may be cut from.
type hintPiece struct {
	text   string
	kind   pieceKind
	target string
}

// wrapHintLine wraps one line of a hint. A numbered step ("1. ...") keeps
// its wrapped lines under its text rather than under its number.
func wrapHintLine(line string, width int) []string {
	numbered := len(line) > len(stepMarker) && line[0] >= '0' && line[0] <= '9' &&
		strings.HasPrefix(line[1:], stepMarker[1:])
	if !numbered || width <= len(stepMarker) {
		return strings.Split(console.WrapText(line, width), "\n")
	}

	indent := strings.Repeat(" ", len(stepMarker))
	wrapped := strings.Split(console.WrapText(line[len(stepMarker):], width-len(stepMarker)), "\n")

	for index := range wrapped {
		lead := indent
		if index == 0 {
			lead = line[:len(stepMarker)]
		}

		wrapped[index] = lead + wrapped[index]
	}

	return wrapped
}

// hintLinks are the URLs a hint mentions, in full.
func hintLinks(hint string) []string {
	var links []string

	for word := range strings.FieldsSeq(hint) {
		if strings.HasPrefix(word, linkPrefix) {
			links = append(links, strings.TrimRight(word, linkTrail))
		}
	}

	return links
}

// hintPieces splits a wrapped line into plain text, button labels, and
// links. quoted carries a label that wrapped onto this line.
func hintPieces(line string, quoted bool, links []string) ([]hintPiece, bool) {
	var (
		pieces []hintPiece
		run    strings.Builder
	)

	flush := func() {
		if run.Len() == 0 {
			return
		}

		kind := piecePlain
		if quoted {
			kind = pieceLabel
		}

		pieces = append(pieces, hintPiece{text: run.String(), kind: kind, target: ""})
		run.Reset()
	}

	for rest := line; rest != ""; {
		if strings.HasPrefix(rest, linkPrefix) {
			flush()

			link, after := linkPieces(rest, links)
			pieces = append(pieces, link...)
			rest = after

			continue
		}

		char, size := utf8.DecodeRuneInString(rest)
		rest = rest[size:]

		switch char {
		case quoteOpen:
			run.WriteRune(char)
			flush()

			quoted = true
		case quoteClose:
			flush()
			run.WriteRune(char)

			quoted = false
		default:
			run.WriteRune(char)
		}
	}

	flush()

	return pieces, quoted
}

// linkPieces takes the URL that text starts with, and any punctuation after
// it, and returns the rest of text. A URL a narrow screen cut short still
// links to the full one.
func linkPieces(text string, links []string) ([]hintPiece, string) {
	end := strings.IndexByte(text, ' ')
	if end < 0 {
		end = len(text)
	}

	word := text[:end]
	shown := strings.TrimRight(word, linkTrail)
	target := shown

	for _, link := range links {
		if strings.HasPrefix(link, shown) {
			target = link

			break
		}
	}

	pieces := []hintPiece{{text: shown, kind: pieceLink, target: target}}
	if trail := word[len(shown):]; trail != "" {
		pieces = append(pieces, hintPiece{text: trail, kind: piecePlain, target: ""})
	}

	return pieces, text[end:]
}

// paintHint draws wrapped hint lines: labels in label style, links
// underlined and clickable, the rest in plain style.
func paintHint(lines []string, links []string, plain, label lipgloss.Style) []string {
	painted := make([]string, 0, len(lines))
	quoted := false

	for _, line := range lines {
		var (
			out    strings.Builder
			pieces []hintPiece
		)

		pieces, quoted = hintPieces(line, quoted, links)

		for _, piece := range pieces {
			switch piece.kind {
			case pieceLabel:
				out.WriteString(label.Render(piece.text))
			case pieceLink:
				out.WriteString(ansi.SetHyperlink(piece.target) + plain.Underline(true).Render(piece.text) +
					ansi.ResetHyperlink())
			case piecePlain:
				out.WriteString(plain.Render(piece.text))
			}
		}

		painted = append(painted, out.String())
	}

	return painted
}
