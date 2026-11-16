package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const (
	// otherImage is a release a config could ask for while the image was a
	// setting.
	otherImage = "ubuntu-24.04"
	gitName    = "Ada Lovelace"
)

// oldConfig is a config from before the image stopped being a setting.
func oldConfig(t *testing.T, image string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), userConfigName)
	writeTestFile(t, path, `
[git]
name = "`+gitName+`"

[hetzner]
type = "`+DefaultType+`"
image = "`+image+`"
`)

	return path
}

// What init writes names no image, and is still all create needs.
func TestRenderedConfigNamesNoImage(t *testing.T) {
	t.Parallel()

	body := Render(Config{
		GitSSHKeyPath: "/keys/git_ed25519",
		GitUserName:   gitName,
		GitUserEmail:  "ada@example.com",
		FirewallName:  "buildbox-tailscale-only",
		ServerType:    DefaultType,
		ServerLoc:     SeedLocation,
		Tag:           DefaultTag,
	})
	if strings.Contains(body, "image") {
		t.Fatalf("the image is not a setting:\n%s", body)
	}

	path := filepath.Join(t.TempDir(), userConfigName)
	writeTestFile(t, path, body)

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	err = cfg.RequireCreate()
	if err != nil {
		t.Fatal(err)
	}
}

// make config copies the example into place, so it has to be a file create
// accepts as it stands.
func TestTheExampleConfigLoads(t *testing.T) {
	t.Parallel()

	example, err := os.ReadFile(filepath.Join("..", "..", "buildbox.toml.example"))
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(example), "image") {
		t.Fatalf("the image is not a setting:\n%s", example)
	}

	path := filepath.Join(t.TempDir(), userConfigName)

	err = os.WriteFile(path, example, ConfigPerm) // #nosec G703 -- a copy under t.TempDir().
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	err = cfg.RequireCreate()
	if err != nil {
		t.Fatal(err)
	}
}

// Every config written before this one names an image. The one every box
// boots must not turn into an error on upgrade.
func TestLoadFileAcceptsTheImageEveryBoxBoots(t *testing.T) {
	t.Parallel()

	cfg, err := LoadFile(oldConfig(t, Image))
	if err != nil {
		t.Fatal(err)
	}

	if cfg.GitUserName != gitName || cfg.ServerType != DefaultType {
		t.Fatalf("cfg = %+v", cfg)
	}
}

// Booting Image for a config that asks for another release would be a
// default nobody chose, so create stops. init still reads the file, since
// writing it again is how the line goes away.
func TestLoadFileRefusesAnotherImage(t *testing.T) {
	t.Parallel()

	path := oldConfig(t, otherImage)

	_, err := LoadFile(path)
	if err == nil {
		t.Fatal("create would boot an image the config did not ask for")
	}

	for _, want := range []string{configKeyServerImage, strconv.Quote(otherImage), Image, path, " init"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("missing %q in:\n%s", want, err.Error())
		}
	}

	seed, err := LoadSeed(path)
	if err != nil {
		t.Fatal(err)
	}

	if seed.GitUserName != gitName || seed.ServerType != DefaultType {
		t.Fatalf("seed = %+v", seed)
	}
}
