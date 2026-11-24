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

	// The two files the welcome reads to learn the state of a box.
	updatesNote = "/var/lib/update-notifier/updates-available"
	restartFlag = "/run/reboot-required"
)

// The welcome is a login shell's doing, and a login shell also runs every
// command, scp, and rsync, create's own among them. One word printed there
// breaks them, so the hook stays behind an interactive shell on a terminal,
// and behind the two users it has a welcome for: root gets none.
func TestWelcomeIsForInteractiveLoginsOfTheTwoUsers(t *testing.T) {
	t.Parallel()

	doc, err := Render(config.Config{Tag: config.DefaultTag}, "build-x", "tskey")
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"  - path: /etc/profile.d/buildbox-welcome.sh\n    permissions: \"0644\"\n",
		"      case $- in\n        *i*)\n",
		`          if [ -z "${TMUX:-}${STY:-}" ] && [ -t 1 ]; then` + "\n" +
			"            case $(id -un) in\n" +
			"              agent) /usr/local/lib/buildbox/welcome agent ;;\n" +
			"              admin) /usr/local/lib/buildbox/welcome admin ;;\n" +
			"            esac\n",
		"  - path: /usr/local/lib/buildbox/welcome\n    permissions: \"0755\"\n",
		// Ubuntu's own welcome is hushed for both, since ours takes its place.
		"  - ['runuser', '--user', 'agent', '--', 'touch', '/home/agent/.hushlogin']\n" +
			"  - ['runuser', '--user', 'admin', '--', 'touch', '/home/admin/.hushlogin']\n",
		// keychain would otherwise follow the welcome with four lines of its own.
		"      eval $(keychain --quiet --eval --agents ssh " + config.GitKeyName + ")\n",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("missing %q\n%s", want, doc)
		}
	}
}

// What create fixes in the rows: the commands of the chosen tools, and ssh
// to the name the tailnet has for the box, which each user's welcome gives
// for the other user. Only admin can restart the box, and is already on it.
func TestWelcomeNamesTheChosenToolsAndTheWayToTheOtherUser(t *testing.T) {
	t.Parallel()

	_, script := welcome([]string{config.ToolCodex, config.ToolAntigravity, config.ToolClaude}, "build-x")

	for _, want := range []string{
		"tools=\"${code}claude$plain, ${code}codex$plain, ${code}agy$plain\"\n",
		"row Docker 'rootless, as agent'\n",
		"row Sudo \"admin has it: ${code}ssh admin@build-x$plain\"\n",
		"  restart 'ssh admin@build-x sudo reboot'\n",
		"  restart 'sudo reboot'\n",
		"  row Agent \"works without sudo: ${code}ssh agent@build-x$plain\"\n",
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

// What the script draws for the agent user, here without color: the wordmark
// as it was designed, with the x's loose arm at the top right, then the
// tagline and the rows. It runs on Linux alone, since the Box row reads what
// only Linux has.
func TestWelcomeScriptDrawsTheWordmark(t *testing.T) {
	t.Parallel()

	script := welcomeOn(t, "", true)
	plain := runWelcome(t, script, config.AgentUser, "NO_COLOR=1", "TERM=xterm-256color")

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
		"\n  Sudo       admin has it: ssh admin@build-x\n" +
			"  Restart    needed by an update: ssh admin@build-x sudo reboot\n\n",
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
	colored := runWelcome(t, script, config.AgentUser, "NO_COLOR=", "TERM=xterm-256color")

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

	if dumb := runWelcome(t, script, config.AgentUser, "NO_COLOR=", "TERM=dumb"); dumb != plain {
		t.Fatalf("a terminal without color got:\n%q", dumb)
	}
}

// What admin gets: the same wordmark, in brass so that the login with sudo
// is not taken for the agent's, a tagline that says who is logged in, and
// rows for what only admin can do. None of the agent's rows are among them.
func TestWelcomeScriptForAdmin(t *testing.T) {
	t.Parallel()

	note := "\nExpanded Security Maintenance for Applications is not enabled.\n\n" +
		"12 updates can be applied immediately.\n" +
		"3 of these updates are standard security updates.\n" +
		"To see these additional updates run: apt list --upgradable\n\n"
	script := welcomeOn(t, note, true)
	plain := runWelcome(t, script, config.AdminUser, "NO_COLOR=1", "TERM=xterm-256color")

	head := "  ███████▀  ▀███████  ██  ██  ▀███████  ███████▀  ▀██████▀  ▄██▀  ▀██▄\n\n" +
		"  You are admin, the user with sudo on this box.\n\n" +
		"  Box        "
	rows := "\n  Packages   sudo apt install NAME\n" +
		"  Updates    12 waiting, 3 for security: sudo apt upgrade\n" +
		"  Restart    needed by an update: sudo reboot\n" +
		"  Agent      works without sudo: ssh agent@build-x\n\n"

	// Between the two is the Box row's own text and no other row.
	_, after, found := strings.Cut(plain, head)
	if box, cut := strings.CutSuffix(after, rows); !found || !cut || strings.Contains(box, "\n") {
		t.Fatalf("admin's welcome without color:\n%s", plain)
	}

	// Brass at the left, cream at the last loose pixel, and a brown shadow.
	colored := runWelcome(t, script, config.AdminUser, "NO_COLOR=", "TERM=xterm-256color")

	for _, sequence := range []string{
		"\x1b[38;2;214;150;52m█\x1b[0m",
		"\x1b[38;2;248;232;200m▀\x1b[0m\n",
		"\x1b[38;2;92;66;24m▀\x1b[0m",
		";48;2;92;66;24m▀\x1b[0m",
		"\x1b[33msudo reboot\x1b[0m\n",
	} {
		if !strings.Contains(colored, sequence) {
			t.Fatalf("missing %q\n%q", sequence, colored)
		}
	}
}

// The Updates row is the count in the note apt leaves, which is worded for
// people: one update or several, with the security updates among them. A
// box with none waiting, without the note, or with a note worded another
// way gets no row, and no complaint in its place. The ESM line has a count
// of its own, which is not one of updates that wait.
func TestAdminsUpdatesRowFollowsAptsNote(t *testing.T) {
	t.Parallel()

	for _, check := range []struct{ note, row string }{
		{
			"1 update can be applied immediately.\n1 of these updates is a standard security update.\n",
			"  Updates    1 waiting, 1 for security: sudo apt upgrade\n",
		},
		{"\n3 updates can be applied immediately.\n", "  Updates    3 waiting: sudo apt upgrade\n"},
		{"0 updates can be applied immediately.\n\n1 additional security update can be applied with ESM Apps.\n", ""},
		{"3 Aktualisierungen können sofort angewendet werden.\n", ""},
		{"", ""},
	} {
		plain := runWelcome(t, welcomeOn(t, check.note, false), config.AdminUser, "NO_COLOR=1", "TERM=dumb")

		want := "\n  Packages   sudo apt install NAME\n" + check.row +
			"  Agent      works without sudo: ssh agent@build-x\n\n"
		if !strings.HasSuffix(plain, want) {
			t.Fatalf("after the note %q:\n%s", check.note, plain)
		}
	}
}

// welcomeOn is the script for a box in the state a test names: the note apt
// leaves about updates, none when empty, and whether Ubuntu asks for a
// restart. The script names both files outright, so it is pointed at the
// test's own, and the machine that runs the test has no say.
func welcomeOn(t *testing.T, note string, restart bool) string {
	t.Helper()

	_, script := welcome([]string{config.ToolOpenCode}, "build-x")
	if !strings.Contains(script, updatesNote) || !strings.Contains(script, restartFlag) {
		t.Fatalf("the script reads other files than %s and %s", updatesNote, restartFlag)
	}

	dir := t.TempDir()
	notePath := filepath.Join(dir, "updates-available")
	flagPath := filepath.Join(dir, "reboot-required")

	if note != "" {
		writeFile(t, notePath, note)
	}

	if restart {
		writeFile(t, flagPath, "")
	}

	return strings.NewReplacer(updatesNote, notePath, restartFlag, flagPath).Replace(script)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()

	err := os.WriteFile(path, []byte(content), 0o600)
	if err != nil {
		t.Fatal(err)
	}
}

// runWelcome runs the script the way the hook does for user and returns
// what it prints. A word on stderr fails the test: at a login it would be a
// line of noise above the prompt.
func runWelcome(t *testing.T, script, user string, env ...string) string {
	t.Helper()

	bash, err := exec.LookPath("bash")
	if err != nil || runtime.GOOS != "linux" {
		t.Skip("the welcome runs on a box: Linux with bash")
	}

	path := filepath.Join(t.TempDir(), "welcome")
	writeFile(t, path, script)

	var stderr strings.Builder

	// #nosec G204 -- bash from the PATH reads a file this test wrote.
	cmd := exec.CommandContext(t.Context(), bash, path, user)

	cmd.Env = append(os.Environ(), env...)
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil || stderr.Len() > 0 {
		t.Fatalf("%v\n%s\n%s", err, out, stderr.String())
	}

	return string(out)
}
