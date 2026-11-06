package wizard

import (
	"strings"
	"testing"

	"github.com/hkluke/buildbox/internal/tailscale"
)

func TestHintPiecesFindLabelsAndLinks(t *testing.T) {
	t.Parallel()

	links := hintLinks("Open " + tailscale.KeysURL + ": then click “Generate access token”.")
	if len(links) != 1 || links[0] != tailscale.KeysURL {
		t.Fatalf("links = %q", links)
	}

	// The narrow screen cut the URL short; it still links to all of it.
	pieces, quoted := hintPieces("Open https://console.tail…: click “Generate", false, links)
	want := []hintPiece{
		{text: "Open ", kind: piecePlain, target: ""},
		{text: "https://console.tail", kind: pieceLink, target: tailscale.KeysURL},
		{text: "…:", kind: piecePlain, target: ""},
		{text: " click “", kind: piecePlain, target: ""},
		{text: "Generate", kind: pieceLabel, target: ""},
	}

	if !quoted || len(pieces) != len(want) {
		t.Fatalf("quoted %v pieces %+v", quoted, pieces)
	}

	for index, piece := range pieces {
		if piece != want[index] {
			t.Fatalf("piece %d = %+v, want %+v", index, piece, want[index])
		}
	}
}

func TestAWrappedLabelStaysALabelOnTheNextLine(t *testing.T) {
	t.Parallel()

	rest, quoted := hintPieces("access token”, with an expiry", true, nil)
	if quoted || len(rest) != 2 || rest[0].kind != pieceLabel || rest[0].text != "access token" {
		t.Fatalf("quoted %v rest %+v", quoted, rest)
	}
}

func TestTheTokenScreenLinksToTheKeysPage(t *testing.T) {
	t.Parallel()

	view := screenAt(stepToken).View()
	if !strings.Contains(view, "\x1b]8;;"+tailscale.KeysURL+"\x07") {
		t.Fatal("the keys page is not a hyperlink")
	}

	assertViewContains(t, stripANSI(view), "2. Click “Generate access token” and set the expiry to 1 day.")
}
