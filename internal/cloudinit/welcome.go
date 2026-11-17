package cloudinit

import (
	"slices"
	"strings"

	"github.com/hkluke/buildbox/internal/config"
)

const (
	// welcomeHookPath is among the files /etc/profile reads for every login
	// shell, whatever becomes of the agent user's own dotfiles.
	welcomeHookPath = "/etc/profile.d/buildbox-welcome.sh"

	// welcomeScriptPath is off the PATH: only the hook runs it.
	welcomeScriptPath = "/usr/local/lib/buildbox/welcome"

	// contentIndent is how deep the content of a write_files entry sits.
	contentIndent = "      "
)

// wordmarkWide is "buildbox" for a terminal of 80 columns or more, two rows
// of pixels to a line of text: # is a letter and : its shadow. The top right
// arm of the x has come loose and drifts away, like the corner of the logo's
// cube. Look at a change on a real terminal, with color and without.
const wordmarkWide = `.........................................................................##.#
.........................................................................##..
##......................##........##..##..............................##.....
##:.................##..##:.......##:.##:.............................##....#
##:.................##:.##:.......##:.##:..........................##........
##:..................::.##:.......##:.##:..........................##.....#..
#######...##....##..##..##:..#######:.#######....######...###..........##....
########..##:...##:.##:.##:.########:.########..########...###..##.....##....
##::::##:.##:...##:.##:.##:.##::::##:.##::::##:.##::::##:...######:..........
##:...##:.##:...##:.##:.##:.##:...##:.##:...##:.##:...##:....####::..........
##:...##:.##:...##:.##:.##:.##:...##:.##:...##:.##:...##:....####:...........
##:...##:.##:...##:.##:.##:.##:...##:.##:...##:.##:...##:...######...........
########:.########:.##:.##:.########:.########:.########:..###::###..........
#######::..#######:.##:.##:..#######:.#######::..######::.###::..###.........
.:::::::....:::::::..::..::...:::::::..:::::::....::::::...:::....:::........
.............................................................................`

// wordmarkNarrow is the init wizard's wordmark, for a terminal too narrow
// for the wide one, such as a phone's.
const wordmarkNarrow = `#.........#.#....#.#.............
#...........#....#.#.............
####.#..#.#.#.####.####.####.#..#
#..#.#..#.#.#.#..#.#..#.#..#..##.
#..#.#..#.#.#.#..#.#..#.#..#..##.
####.####.#.#.####.####.####.#..#`

// welcomeHook starts the welcome. /etc/profile reads it for sh as well as for
// bash, so it keeps to what both understand.
const welcomeHook = `# buildbox's welcome for the __AGENT_USER__ user, at an interactive login and
# nowhere else. A command, scp, and rsync run through a login shell too, and
# a word printed there would end up in their output. tmux and screen start a
# login shell in every pane.
case $- in
  *i*)
    if [ "$(id -un)" = __AGENT_USER__ ] && [ -z "${TMUX:-}${STY:-}" ] && [ -t 1 ]; then
      __SCRIPT__
    fi
    ;;
esac
`

// welcomeScript prints the welcome. It was run on a real box, on a terminal
// with color, without, and at each width; a change to it needs that again,
// since the tests run it on Linux alone and never on a terminal.
const welcomeScript = `#!/bin/bash
# What the __AGENT_USER__ user sees at an interactive login, in place of Ubuntu's
# own welcome, which ~/.hushlogin keeps quiet. The Box row is read at each
# login; everything else was fixed when buildbox created this box.

escape=$'\033'
# Octal, so that the cloud-init document that carries this file stays ASCII:
# the upper half block, the lower half block, the full block, and the middle
# dot.
upper=$'\342\226\200' lower=$'\342\226\204' full=$'\342\226\210' dot=$'\302\267'

# Nothing on the box can ask the terminal which colors it has: TERM reads
# xterm-256color on nearly every one, with 24-bit color or without, and SSH
# does not pass COLORTERM on unless told to. Nearly every terminal has 24-bit
# color, so only one that says it has no color at all gets none.
if [ -n "${NO_COLOR:-}" ] || [ "${TERM:-dumb}" = dumb ]; then
  color='' dim='' bold='' code='' plain=''
else
  color=yes dim="${escape}[2m" bold="${escape}[1m" code="${escape}[33m" plain="${escape}[0m"
fi

# shade sets ink to the color of one pixel, at paint's column: # is the
# wordmark, which runs from teal (#2BB8A0) on the left to mint (#B4ECDF) on
# the right as the wizard's does, : is its shadow, and anything else is the
# terminal's own background. Without color the shadow goes, since it would
# only thicken the letters.
shade() {
  case $1 in
    '#')
      ink="38;2;$((43 + (137 * column + span / 2) / span))"
      ink+=";$((184 + (52 * column + span / 2) / span))"
      ink+=";$((160 + (63 * column + span / 2) / span))"
      ;;
    ':') ink=${color:+38;2;23;87;79} ;;
    *) ink='' ;;
  esac
}

# paint draws the picture on its input. A line of text holds two rows of
# pixels, one in the upper half of each cell and one in the lower, so the
# pixels come out square.
paint() {
  local above below line span column ink over under block colors

  while IFS= read -r above && IFS= read -r below; do
    span=$((${#above} - 1)) line=''

    for ((column = 0; column <= span; column++)); do
      shade "${above:column:1}"
      over=$ink
      shade "${below:column:1}"
      under=$ink

      case ${over:+o}${under:+u} in
        o) block=$upper colors=$over ;;
        u) block=$lower colors=$under ;;
        ou)
          if [ "$over" = "$under" ]; then
            block=$full colors=$over
          else
            block=$upper colors="$over;${under/#38/48}"
          fi
          ;;
        *)
          line+=' '
          continue
          ;;
      esac

      line+="${color:+${escape}[${colors}m}$block$plain"
    done

    # Without its shadow the picture's last line is empty.
    line=${line%"${line##*[! ]}"}
    if [ -n "$line" ]; then
      printf '  %s\n' "$line"
    fi
  done
}

row() {
  printf '  %s%-11s%s%s\n' "$dim" "$1" "$plain" "$2"
}

# The wide picture is 79 columns with its margin and the narrow one 35. A
# session that came without a window size says 0 and gets the wide one.
columns=$(stty size 2>/dev/null) columns=${columns#* }

if [ "${columns:-0}" -eq 0 ] || [ "$columns" -ge 80 ]; then
  echo
  paint <<'PICTURE'
__WIDE__
PICTURE
elif [ "$columns" -ge 36 ]; then
  echo
  paint <<'PICTURE'
__NARROW__
PICTURE
fi

printf '\n  %s\n\n' "${dim}Let your agent think inside the box.$plain"

box="$bold$(hostname)$plain $dot $(. /etc/os-release && printf '%s' "$PRETTY_NAME")"

cores="$(nproc) cores"
box+=" $dot ${cores/#1 cores/1 core}"

# Installed memory is the size the box was rented with. MemTotal is what the
# kernel left of it, which rounds to another number on a large box.
memory=$(lsmem --bytes --summary=only 2>/dev/null | sed --quiet 's/^Total online memory: *//p')
if [ -n "$memory" ]; then
  box+=" $dot $(((memory + (1 << 29)) >> 30)) GB"
fi

free=$(df --output=avail --block-size=G /)
box+=" $dot ${free//[!0-9]/} GB free"

row Box "$box"
row Projects '~/projects'

tools="__TOOLS__"
if [ -n "$tools" ]; then
  row Tools "$tools"
fi

row Docker 'rootless, as __AGENT_USER__'
row Sudo "__ADMIN_USER__ has it: ${code}ssh __ADMIN_USER__@__NAME__$plain"

# Ubuntu raises this flag when an update, a new kernel for one, takes effect
# only after a restart. It is the one line of Ubuntu's welcome that is kept.
if [ -e /run/reboot-required ]; then
  row Restart "needed by an update: ${code}ssh __ADMIN_USER__@__NAME__ sudo reboot$plain"
fi

echo
`

// welcome is the hook and the script, filled in. name is the box as the
// tailnet has it, which is the name ssh takes.
func welcome(ids []string, name string) (string, string) {
	fill := strings.NewReplacer(
		"__SCRIPT__", welcomeScriptPath,
		"__WIDE__", wordmarkWide,
		"__NARROW__", wordmarkNarrow,
		"__TOOLS__", welcomeTools(ids),
		"__AGENT_USER__", config.AgentUser,
		"__ADMIN_USER__", config.AdminUser,
		"__NAME__", name,
	)

	return fill.Replace(welcomeHook), fill.Replace(welcomeScript)
}

// welcomeTools names the commands that start the chosen coding tools, each
// in the color the script gives a command, and is empty when none was chosen.
func welcomeTools(ids []string) string {
	var commands []string

	for _, tool := range config.Tools() {
		if slices.Contains(ids, tool.ID) {
			commands = append(commands, "${code}"+tool.Binary+"$plain")
		}
	}

	return strings.Join(commands, ", ")
}

// welcomeFiles are the write_files entries for the hook and the script.
func welcomeFiles(ids []string, name string) string {
	hook, script := welcome(ids, name)

	return `  # The agent user's welcome at login: the wordmark and what the box holds.
  # Every login shell reads the first file, which runs the second for an
  # interactive login of that user alone, so a command, scp, and rsync get
  # no word of it. Both are ASCII: the script spells its block characters in
  # octal.
  - path: ` + welcomeHookPath + `
    permissions: "0644"
    content: |
` + contentBlock(hook) + `  - path: ` + welcomeScriptPath + `
    permissions: "0755"
    content: |
` + strings.TrimSuffix(contentBlock(script), "\n")
}

// contentBlock is text as the content of a write_files entry. An empty line
// stays empty, so the document has no trailing spaces.
func contentBlock(text string) string {
	var block strings.Builder

	for line := range strings.Lines(text) {
		if line != "\n" {
			block.WriteString(contentIndent)
		}

		block.WriteString(line)
	}

	return block.String()
}
