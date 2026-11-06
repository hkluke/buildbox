package cloudinit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hkluke/buildbox/internal/config"
)

// The golden files are the exact user-data a new server boots with, the
// document the security model rests on. After an intended change, rewrite
// them with UPDATE_GOLDEN=1 go test ./internal/cloudinit and review the diff.
func TestUserDataMatchesGolden(t *testing.T) {
	t.Parallel()

	for _, check := range []struct {
		name  string
		tools string
	}{
		{"default", ""},
		{"codex-claude", config.ToolCodex + " " + config.ToolClaude},
		{"none", config.ToolNone},
	} {
		t.Run(check.name, func(t *testing.T) {
			t.Parallel()

			doc, err := Render(
				config.Config{AITools: check.tools, Tag: config.DefaultTag},
				"build-x", "tskey-auth-SAMPLE",
			)
			if err != nil {
				t.Fatal(err)
			}

			assertGolden(t, filepath.Join("testdata", check.name+".golden"), doc)
		})
	}
}

func assertGolden(t *testing.T, path, got string) {
	t.Helper()

	if os.Getenv("UPDATE_GOLDEN") != "" {
		err := os.WriteFile(path, []byte(got), 0o600)
		if err != nil {
			t.Fatal(err)
		}

		return
	}

	want, err := os.ReadFile(path) // #nosec G304 -- a file under testdata named by this test.
	if err != nil {
		t.Fatalf("%v\nCreate it with: UPDATE_GOLDEN=1 go test ./internal/cloudinit", err)
	}

	if got != string(want) {
		t.Fatalf("user-data differs from %s at %s\n"+
			"If the change is intended: UPDATE_GOLDEN=1 go test ./internal/cloudinit", path, firstDifference(string(want), got))
	}
}

// firstDifference names the first line that differs, which is where a
// reviewer should look.
func firstDifference(want, got string) string {
	wantLines := strings.Split(want, "\n")
	gotLines := strings.Split(got, "\n")

	for index := range max(len(wantLines), len(gotLines)) {
		var wantLine, gotLine string
		if index < len(wantLines) {
			wantLine = wantLines[index]
		}

		if index < len(gotLines) {
			gotLine = gotLines[index]
		}

		if wantLine != gotLine {
			return fmt.Sprintf("line %d:\n  want %q\n  got  %q", index+1, wantLine, gotLine)
		}
	}

	return "the end of the file"
}
