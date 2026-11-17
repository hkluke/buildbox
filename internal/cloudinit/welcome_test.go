package cloudinit

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/hkluke/buildbox/internal/config"
)

const (
	// hetznerUserDataLimit is the most user-data Hetzner takes for a server.
	hetznerUserDataLimit = 32 << 10

	// welcomeMargin is the two spaces paint puts before every line.
	welcomeMargin = 2

	lastASCII = 127
)

// The welcome is a login shell's doing, and a login shell also runs every
// command, scp, and rsync, create's own among them. One word printed there
// breaks them, so the hook stays behind an interactive shell on a terminal,
// and behind the agent user, whose welcome it is.
func TestWelcomeIsForTheAgentUsersInteractiveLogins(t *testing.T) {
	t.Parallel()

	doc, err := Render(config.Config{Tag: config.DefaultTag}, "build-x", "tskey")
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"  - path: /etc/profile.d/buildbox-welcome.sh\n    permissions: \"0644\"\n",
		"      case $- in\n        *i*)\n",
		`if [ "$(id -un)" = agent ] && [ -z "${TMUX:-}${STY:-}" ] && [ -t 1 ]; then` + "\n" +
			"            /usr/local/lib/buildbox/welcome\n",
		"  - path: /usr/local/lib/buildbox/welcome\n    permissions: \"0755\"\n",
		// Ubuntu's own welcome is hushed for the agent user alone.
		"  - ['runuser', '--user', 'agent', '--', 'touch', '/home/agent/.hushlogin']\n",
		// keychain would otherwise follow the welcome with four lines of its own.
		"      eval $(keychain --quiet --eval --agents ssh id_ed25519)\n",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("missing %q\n%s", want, doc)
		}
	}
}

// The rows below Box are fixed at create: the commands of the chosen tools,
// and ssh to the name the tailnet has for the box.
func TestWelcomeNamesTheChosenToolsAndTheWayToAdmin(t *testing.T) {
	t.Parallel()

	_, script := welcome([]string{config.ToolCodex, config.ToolAntigravity, config.ToolClaude}, "build-x")

	for _, want := range []string{
		"tools=\"${code}claude$plain, ${code}codex$plain, ${code}agy$plain\"\n",
		"row Docker 'rootless, as agent'\n",
		"row Sudo \"admin has it: ${code}ssh admin@build-x$plain\"\n",
		"  row Restart \"needed by an update: ${code}ssh admin@build-x sudo reboot$plain\"\n",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("missing %q\n%s", want, script)
		}
	}

	_, bare := welcome(nil, "build-x")
	if !strings.Contains(bare, "tools=\"\"\n") {
		t.Fatalf("tools on a box without any:\n%s", bare)
	}
}

// paint pairs rows of pixels into lines of text and colors a pixel by its
// column, so a picture has an even number of rows of one width, and fits the
// terminal the script picks it for with the margin and a column to spare.
func TestWelcomePicturesAreWholeAndFit(t *testing.T) {
	t.Parallel()

	for _, check := range []struct {
		name     string
		picture  string
		terminal int
	}{
		{"wide", wordmarkWide, 80},
		{"narrow", wordmarkNarrow, 36},
	} {
		rows := strings.Split(check.picture, "\n")
		if len(rows)%2 != 0 {
			t.Fatalf("%s: %d rows, which leaves one without a partner", check.name, len(rows))
		}

		for index, row := range rows {
			if len(row) != len(rows[0]) || strings.Trim(row, "#:.") != "" {
				t.Fatalf("%s: row %d is %q", check.name, index+1, row)
			}
		}

		if welcomeMargin+len(rows[0]) >= check.terminal {
			t.Fatalf("%s: %d columns do not fit a terminal of %d", check.name, len(rows[0]), check.terminal)
		}

		if !strings.Contains(welcomeScript, `"$columns" -ge `+strconv.Itoa(check.terminal)+" ]") {
			t.Fatalf("%s: the script does not pick a picture at %d columns", check.name, check.terminal)
		}
	}
}

// The document goes through the Hetzner API and its metadata service before
// cloud-init reads it, and has always been plain ASCII on that way; the
// welcome's block characters are spelled in octal to keep it so. Hetzner
// also takes no more than 32 KiB, and the welcome is half of what is sent.
func TestUserDataStaysASCIIAndUnderHetznersLimit(t *testing.T) {
	t.Parallel()

	every := make([]string, 0, len(config.Tools()))
	for _, tool := range config.Tools() {
		every = append(every, tool.ID)
	}

	longest := strings.Repeat("x", 63)

	doc, err := Render(config.Config{AITools: strings.Join(every, " "), Tag: config.DefaultTag}, longest, "tskey")
	if err != nil {
		t.Fatal(err)
	}

	for index, char := range doc {
		if char > lastASCII {
			t.Fatalf("%q is not ASCII: %q", char, doc[max(index-60, 0):index])
		}
	}

	if len(doc) >= hetznerUserDataLimit {
		t.Fatalf("the document is %d bytes, and Hetzner takes less than %d", len(doc), hetznerUserDataLimit)
	}
}

// What the script draws, here without color: the wordmark as it was designed,
// with the x's loose arm at the top right, then the tagline and the rows. It
// runs on Linux alone, since the Box row reads what only Linux has. The
// Restart row depends on the machine running the test and is not checked.
func TestWelcomeScriptDrawsTheWordmark(t *testing.T) {
	t.Parallel()

	plain := runWelcome(t, "NO_COLOR=1", "TERM=xterm-256color")

	want := strings.Join([]string{
		"",
		"                                                                           ██ ▀",
		"  ██                  ▄▄  ██        ██  ██                              ██    ▄",
		"  ██                  ▀▀  ██        ██  ██                           ██     ▄",
		"  ███████▄  ██    ██  ██  ██  ▄███████  ███████▄  ▄██████▄  ▀██▄  ▄▄     ██",
		"  ██    ██  ██    ██  ██  ██  ██    ██  ██    ██  ██    ██    ▀████▀",
		"  ██    ██  ██    ██  ██  ██  ██    ██  ██    ██  ██    ██    ▄████▄",
		"  ███████▀  ▀███████  ██  ██  ▀███████  ███████▀  ▀██████▀  ▄██▀  ▀██▄",
		"",
		"  Let your agent think inside the box.",
		"",
		"  Box        ",
	}, "\n")

	if !strings.HasPrefix(plain, want) {
		t.Fatalf("the welcome without color:\n%s", plain)
	}

	for _, row := range []string{
		"\n  Projects   ~/projects\n",
		"\n  Tools      opencode\n",
		"\n  Docker     rootless, as agent\n",
		"\n  Sudo       admin has it: ssh admin@build-x\n",
	} {
		if !strings.Contains(plain, row) {
			t.Fatalf("missing %q\n%s", row, plain)
		}
	}

	if strings.Contains(plain, "\x1b") {
		t.Fatalf("an escape sequence without color:\n%q", plain)
	}

	// With color the wordmark starts in teal at the left and the last loose
	// pixel at the right is mint, a letter casts its shadow into the cell
	// below it, and a command is in the color the console gives commands.
	colored := runWelcome(t, "NO_COLOR=", "TERM=xterm-256color")

	for _, sequence := range []string{
		"\x1b[38;2;43;184;160m█\x1b[0m",
		"\x1b[38;2;180;236;223m▀\x1b[0m\n",
		"\x1b[38;2;23;87;79m▀\x1b[0m",
		";48;2;23;87;79m▀\x1b[0m",
		"\x1b[33mssh admin@build-x\x1b[0m\n",
	} {
		if !strings.Contains(colored, sequence) {
			t.Fatalf("missing %q\n%q", sequence, colored)
		}
	}

	if dumb := runWelcome(t, "NO_COLOR=", "TERM=dumb"); dumb != plain {
		t.Fatalf("a terminal without color got:\n%q", dumb)
	}
}

func runWelcome(t *testing.T, env ...string) string {
	t.Helper()

	bash, err := exec.LookPath("bash")
	if err != nil || runtime.GOOS != "linux" {
		t.Skip("the welcome runs on a box: Linux with bash")
	}

	_, script := welcome([]string{config.ToolOpenCode}, "build-x")
	path := filepath.Join(t.TempDir(), "welcome")

	err = os.WriteFile(path, []byte(script), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	// #nosec G204 -- bash from the PATH reads a file this test wrote.
	cmd := exec.CommandContext(t.Context(), bash, path)

	cmd.Env = append(os.Environ(), env...)

	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	return string(out)
}
