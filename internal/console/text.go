package console

import (
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

// ellipsisLimit is the width below which a cut shows no ellipsis, since the
// ellipsis would take most of what is left.
const ellipsisLimit = 3

// WrapText breaks text into lines of at most width cells, at spaces. A word
// longer than width is cut.
func WrapText(text string, width int) string {
	if width <= 0 || strings.TrimSpace(text) == "" {
		return ""
	}

	words := strings.Fields(text)
	lines := make([]string, 0, len(words))

	var line strings.Builder

	for _, word := range words {
		word = TrimWidth(word, width)
		if line.Len() == 0 {
			line.WriteString(word)

			continue
		}

		if lipgloss.Width(line.String())+1+lipgloss.Width(word) <= width {
			line.WriteByte(' ')
			line.WriteString(word)

			continue
		}

		lines = append(lines, line.String())
		line.Reset()
		line.WriteString(word)
	}

	if line.Len() > 0 {
		lines = append(lines, line.String())
	}

	return strings.Join(lines, "\n")
}

// TrimWidth cuts text to width cells, ending in an ellipsis when it was cut.
func TrimWidth(text string, width int) string {
	if width <= 0 || text == "" {
		return ""
	}

	if lipgloss.Width(text) <= width {
		return text
	}

	if width <= ellipsisLimit {
		return trimRunes(text, width)
	}

	return trimRunes(text, width-1) + "…"
}

func trimRunes(text string, width int) string {
	cut := text

	for lipgloss.Width(cut) > width && cut != "" {
		_, size := utf8.DecodeLastRuneInString(cut)
		cut = cut[:len(cut)-size]
	}

	return cut
}

// PadRight fills text with spaces up to width cells.
func PadRight(text string, width int) string {
	gap := width - lipgloss.Width(text)
	if gap <= 0 {
		return text
	}

	return text + strings.Repeat(" ", gap)
}

// PadLeft fills text with spaces on the left up to width cells, for a
// right-aligned column.
func PadLeft(text string, width int) string {
	gap := width - lipgloss.Width(text)
	if gap <= 0 {
		return text
	}

	return strings.Repeat(" ", gap) + text
}

// splitLines drops the empty string a trailing newline would leave.
func splitLines(content string) []string {
	if content == "" {
		return nil
	}

	parts := strings.Split(content, "\n")
	if strings.HasSuffix(content, "\n") {
		parts = parts[:len(parts)-1]
	}

	return parts
}

// JoinAnd is "A", "A and B", or "A, B, and C".
func JoinAnd(parts []string) string {
	const pair = 2

	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	case pair:
		return parts[0] + " and " + parts[1]
	default:
		return strings.Join(parts[:len(parts)-1], ", ") + ", and " + parts[len(parts)-1]
	}
}
