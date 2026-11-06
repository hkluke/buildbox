package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/hetznercloud/hcloud-go/v2/hcloud"

	"github.com/hkluke/buildbox/internal/abort"
	"github.com/hkluke/buildbox/internal/console"
	"github.com/hkluke/buildbox/internal/hetzner"
	"github.com/hkluke/buildbox/internal/tailscale"
)

const (
	columnGap       = "  "
	columnStatus    = 1
	columnTailscale = 5
	stateRunning    = "running"
	stateOff        = "off"
	tailscaleSelf   = "this machine"
	tailnetPresent  = "present"
	hoursPerDay     = 24
	hoursShown      = 48
)

// buildBox keeps facts, not display text. The table and --json format them
// differently.
type buildBox struct {
	name     string
	status   string
	kind     string
	location string
	created  time.Time
	peer     string
}

// buildBoxRecord is one element of list --json. Its keys are part of the
// interface scripts rely on, so they change only with a reason.
type buildBoxRecord struct {
	Name     string  `json:"name"`
	Status   string  `json:"status"`
	Type     string  `json:"type"`
	Location string  `json:"location"`
	Created  string  `json:"created"`
	Tailnet  string  `json:"tailnet"`
	Address  *string `json:"address"`
}

func (box buildBox) cells(now time.Time) []string {
	return []string{
		box.name, box.status, box.kind, box.location,
		formatAge(now.Sub(box.created)), tailscaleColumn(box.peer),
	}
}

func (box buildBox) record() buildBoxRecord {
	record := buildBoxRecord{
		Name:     box.name,
		Status:   box.status,
		Type:     box.kind,
		Location: box.location,
		Created:  box.created.UTC().Format(time.RFC3339),
		Tailnet:  tailnetPresent,
	}

	switch box.peer {
	case tailscale.PeerAbsent:
		record.Tailnet = tailscale.PeerAbsent
	case tailscale.PeerSelf:
		record.Tailnet = tailscale.PeerSelf
	default:
		address := box.peer
		record.Address = &address
	}

	return record
}

func runList(ctx context.Context, args []string) error {
	asJSON, err := parseListArgs(args)
	if err != nil {
		return err
	}

	_, err = tailscale.RequireUp(ctx)
	if err != nil {
		return err
	}

	client, err := newHetznerClient()
	if err != nil {
		return err
	}

	servers, err := hetzner.ListServers(ctx, client)
	if err != nil {
		return err
	}

	boxes, err := buildRows(ctx, servers)
	if err != nil {
		return err
	}

	if asJSON {
		return printBuildBoxJSON(boxes)
	}

	con := console.New()
	if len(boxes) == 0 {
		con.Writeln(con.Paint.Muted.Render("No build boxes. Create one with ") +
			con.Paint.Code.Render(abort.Prog()+" create"))

		return nil
	}

	printBuildBoxTable(con, boxes, time.Now())

	return nil
}

func parseListArgs(args []string) (bool, error) {
	asJSON := false

	for _, arg := range args {
		switch arg {
		case flagHelp, flagHelpShort:
			return false, showCommandHelp(cmdList)
		case flagJSON:
			asJSON = true
		default:
			return false, abort.Usage("Unknown argument: "+arg, cmdList)
		}
	}

	return asJSON, nil
}

// printBuildBoxJSON prints [] rather than null when there are no boxes, so a
// script can always iterate over the result.
func printBuildBoxJSON(boxes []buildBox) error {
	records := make([]buildBoxRecord, 0, len(boxes))
	for _, box := range boxes {
		records = append(records, box.record())
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")

	err := encoder.Encode(records)
	if err != nil {
		return abort.Newf("Could not write the list as JSON.\n%s", err.Error())
	}

	return nil
}

func buildRows(ctx context.Context, servers []*hcloud.Server) ([]buildBox, error) {
	names := make([]string, 0, len(servers))
	byName := map[string]*hcloud.Server{}

	for _, server := range servers {
		if server == nil {
			continue
		}

		if _, seen := byName[server.Name]; seen {
			continue
		}

		byName[server.Name] = server
		names = append(names, server.Name)
	}

	slices.Sort(names)

	if len(names) == 0 {
		return nil, nil
	}

	boxes := make([]buildBox, 0, len(names))

	namePattern := regexp.MustCompile(`^[A-Za-z0-9.-]+$`)
	for _, name := range names {
		if !namePattern.MatchString(name) {
			return nil, abort.Newf("Refusing to list a build box with an unexpected name: %s", name)
		}

		box, err := serverBox(byName[name])
		if err != nil {
			return nil, err
		}

		box.peer, err = tailscale.PeerState(ctx, name)
		if err != nil {
			return nil, err
		}

		boxes = append(boxes, box)
	}

	return boxes, nil
}

func serverBox(server *hcloud.Server) (buildBox, error) {
	if server.ServerType == nil || server.ServerType.Name == "" || server.Location == nil || server.Location.Name == "" {
		return buildBox{}, abort.Newf("Could not read Hetzner server %s.", server.Name)
	}

	return buildBox{
		name:     server.Name,
		status:   string(server.Status),
		kind:     server.ServerType.Name,
		location: server.Location.Name,
		created:  server.Created,
	}, nil
}

func tailscaleColumn(state string) string {
	switch state {
	case tailscale.PeerSelf:
		return tailscaleSelf
	case tailscale.PeerAbsent:
		return tailscale.PeerAbsent
	default:
		return state
	}
}

// formatAge rounds down, so a box never looks younger than it is.
func formatAge(age time.Duration) string {
	switch {
	case age < time.Minute:
		return "<1m"
	case age < time.Hour:
		return fmt.Sprintf("%dm", int(age/time.Minute))
	case age < hoursShown*time.Hour:
		return fmt.Sprintf("%dh", int(age/time.Hour))
	default:
		return fmt.Sprintf("%dd", int(age/(hoursPerDay*time.Hour)))
	}
}

func printBuildBoxTable(con *console.Console, boxes []buildBox, now time.Time) {
	table := make([][]string, 0, 1+len(boxes))
	table = append(table, []string{"NAME", "STATUS", "TYPE", "LOCATION", "AGE", "TAILSCALE"})

	for _, box := range boxes {
		table = append(table, box.cells(now))
	}

	widths := make([]int, len(table[0]))

	for _, cells := range table {
		for column, cell := range cells {
			widths[column] = max(widths[column], lipgloss.Width(cell))
		}
	}

	for index, cells := range table {
		con.Writeln(tableRow(con.Paint, cells, widths, index == 0))
	}
}

// tableRow pads before it colors, so escape codes never count as width.
func tableRow(paint console.Paint, cells []string, widths []int, header bool) string {
	parts := make([]string, len(cells))

	for column, cell := range cells {
		text := cell
		if column < len(cells)-1 {
			text = console.PadRight(cell, widths[column])
		}

		parts[column] = cellStyle(paint, column, cell, header).Render(text)
	}

	return strings.Join(parts, columnGap)
}

func cellStyle(paint console.Paint, column int, cell string, header bool) lipgloss.Style {
	switch {
	case header:
		return paint.Muted
	case column == 0:
		return paint.Title
	case column == columnStatus:
		return statusStyle(paint, cell)
	case column == columnTailscale:
		return tailscaleStyle(paint, cell)
	default:
		return paint.Text
	}
}

// statusStyle makes anything but running or off stand out, since it means
// the server is changing state.
func statusStyle(paint console.Paint, status string) lipgloss.Style {
	switch status {
	case stateRunning:
		return paint.OK
	case stateOff:
		return paint.Faint
	default:
		return paint.Warn
	}
}

func tailscaleStyle(paint console.Paint, state string) lipgloss.Style {
	switch state {
	case tailscale.PeerAbsent:
		return paint.Faint
	case tailscaleSelf:
		return paint.Warn
	default:
		return paint.Text
	}
}
