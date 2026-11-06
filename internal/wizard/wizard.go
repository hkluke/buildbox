// Package wizard is buildbox init: a full-screen form, built with Bubble Tea,
// that walks through the config and shows Hetzner's live offers.
package wizard

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/muesli/termenv"

	"github.com/hkluke/buildbox/internal/config"
	"github.com/hkluke/buildbox/internal/console"
	"github.com/hkluke/buildbox/internal/hetzner"
	"github.com/hkluke/buildbox/internal/tailscale"
)

type stepID int

// The steps, grouped by section. The hub lists the sections and is where
// the config is written.
const (
	stepLoad stepID = iota
	stepHub
	stepLocation
	stepMachine
	stepImage
	stepGitKey
	stepGitIdentity
	stepTools
	stepAccess
	stepToken
	stepPreview
	stepPolicy
	stepKeys
	stepOAuthID
	stepOAuthSecret
)

const (
	keyEnter       = "enter"
	keyOpen        = "o"
	keyOpenPage    = "ctrl+o"
	keyCopy        = "c"
	gitChoiceUse   = "use"
	gitChoiceNew   = "new"
	gitChoicePath  = "path"
	keysChoiceMint = "mint"
	keysChoiceAsk  = "ask"
	accessToken    = "token"
	accessManual   = "manual"
	hubBox         = "box"
	hubAgent       = "agent"
	hubTailnet     = "tailnet"
	hubWrite       = "write"
	listHome       = -1 << 30
	listEnd        = 1 << 30
)

const (
	fieldWidthMax  = 72
	fieldWidthMin  = 20
	pathFieldLimit = 512
	fallbackWidth  = 80
	fallbackHeight = 24
	fieldChrome    = 6
)

type catalogReady struct {
	cat hetzner.Catalog
}

type catalogFailed struct {
	err error
}

// clientChecked is the result of trying the OAuth client against the
// Tailscale API.
type clientChecked struct {
	err error
}

// seedChecked is the result of trying the OAuth client found in the config
// while the catalog loads.
type seedChecked struct {
	err error
}

// setupPlanned is the policy read with the access token, and what it lacks.
type setupPlanned struct {
	policy tailscale.Policy
	plan   tailscale.SetupPlan
	err    error
}

// setupApplied is the result of writing the policy and creating the client.
type setupApplied struct {
	client tailscale.OAuthClient
	plan   tailscale.SetupPlan
	err    error
}

// Model is the init form. Run it with tea.NewProgram, then read the result
// with Confirmed, Fatal, Config, NeedsFirewall, and NewGitPublicKey.
type Model struct {
	step    stepID
	width   int
	height  int
	spin    spinner.Model
	drift   int
	color   bool
	updates tea.Cmd
	update  updateFound

	// saved is the config as init started, plus an OAuth client saved since;
	// a difference means there is something Write has not kept yet.
	saved      config.Config
	askingQuit bool
	input      textinput.Model
	email      textinput.Model
	paint      console.Paint
	cat        hetzner.Catalog
	dest       string
	replacing  bool
	load       tea.Cmd
	tailnet    tailscale.Tailnet

	// guided is a first run before its review: the sections follow one
	// another from the welcome. begun is set once the welcome or the loading
	// screen is left, so quitting before that never asks.
	guided    bool
	begun     bool
	seedFound bool

	tag             string
	accessChoice    string
	token           string
	policy          tailscale.Policy
	plan            tailscale.SetupPlan
	narrow          bool
	setUp           bool
	seedPending     bool
	loaded          bool
	tailnetDecided  bool
	hubChoice       string
	focusEmail      bool
	keysChoice      string
	oauthID         string
	oauthSecret     string
	checking        bool
	busy            string
	gitKey          string
	gitChoice       string
	gitSuggest      string
	gitEditing      bool
	gitPublicKey    string
	gitName         string
	gitEmail        string
	tools           string
	toolChoice      string
	image           string
	location        string
	serverType      string
	suggestImage    string
	suggestLocation string
	suggestMachine  string

	archFilter string
	cpuFilter  string
	note       string
	quit       bool
	confirmed  bool
	fatal      string
}

// New starts the form from seed, the current config or defaults. tailnet is
// what the local Tailscale app says about this machine. load fetches the
// catalog; LoadCatalog makes one.
func New(seed config.Config, dest string, replacing bool, tailnet tailscale.Tailnet, load tea.Cmd) Model {
	paint := console.AdaptivePaint()
	spin := spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(paint.Accent))

	tag := seed.Tag
	if config.InvalidTag(tag) != "" {
		tag = config.DefaultTag
	}

	return Model{
		step:         stepLoad,
		width:        fallbackWidth,
		height:       fallbackHeight,
		spin:         spin,
		color:        lipgloss.ColorProfile() != termenv.Ascii,
		input:        newField(paint),
		email:        newField(paint),
		paint:        paint,
		dest:         dest,
		replacing:    replacing,
		load:         load,
		tailnet:      tailnet,
		tag:          tag,
		guided:       !replacing,
		seedFound:    seed.OAuthClientID != "" && seed.OAuthClientSecret != "",
		accessChoice: accessToken,
		narrow:       true,
		seedPending:  seed.OAuthClientID != "" && seed.OAuthClientSecret != "",
		keysChoice:   keysChoiceMint,
		oauthID:      seed.OAuthClientID,
		oauthSecret:  seed.OAuthClientSecret,
		gitKey:       seed.GitSSHKeyPath,
		gitName:      seed.GitUserName,
		gitEmail:     seed.GitUserEmail,
		tools:        seed.AITools,
		image:        seed.ServerImage,
		location:     seed.ServerLoc,
		serverType:   seed.ServerType,
	}
}

// Init starts the spinner, the catalog load, and the check of the OAuth
// client the config came with, if any.
func (wiz Model) Init() tea.Cmd {
	cmds := []tea.Cmd{wiz.spin.Tick, wiz.load, wiz.updates}

	if wiz.seedPending {
		client := tailscale.OAuthClient{ID: wiz.oauthID, Secret: wiz.oauthSecret}
		cmds = append(cmds, checkSeed(client, wiz.tag))
	}

	return tea.Batch(cmds...)
}

// Update handles one key, resize, tick, or background result.
func (wiz Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		wiz.width = msg.Width
		wiz.height = msg.Height
		wiz.input.Width = wiz.fieldWidth()
		wiz.email.Width = wiz.fieldWidth()

		return wiz, nil
	case spinner.TickMsg:
		return wiz.onTick(msg)
	case catalogReady:
		return wiz.onReady(msg)
	case catalogFailed:
		wiz.fatal = "Could not read Hetzner locations and prices.\n" + msg.err.Error()

		return wiz, tea.Quit
	case seedChecked, clientChecked, setupPlanned, setupApplied:
		next, cmd := wiz.onResult(msg)

		return next, cmd
	case browserOpened, copied, updateFound, clientSaved:
		return wiz.onNotice(msg), nil
	case tea.KeyMsg:
		next, cmd := wiz.onKey(msg)

		return next, cmd
	default:
		return wiz, nil
	}
}

// Config is the config the form produced.
func (wiz Model) Config() config.Config {
	return config.Config{
		GitSSHKeyPath:     wiz.gitKey,
		GitUserName:       wiz.gitName,
		GitUserEmail:      wiz.gitEmail,
		FirewallName:      hetzner.FirewallName,
		ServerType:        wiz.serverType,
		ServerImage:       wiz.image,
		ServerLoc:         wiz.location,
		AITools:           config.StoredTools(wiz.tools),
		Tag:               wiz.tag,
		OAuthClientID:     wiz.oauthID,
		OAuthClientSecret: wiz.oauthSecret,
	}
}

// Confirmed reports whether the user chose Write on the hub.
func (wiz Model) Confirmed() bool {
	return wiz.confirmed
}

// Fatal is the error that ended the form, such as a failed catalog load, or
// empty.
func (wiz Model) Fatal() string {
	return wiz.fatal
}

// NeedsFirewall reports whether buildbox's firewall was missing, so init
// still has to create it.
func (wiz Model) NeedsFirewall() bool {
	return !wiz.cat.Firewall.Exists
}

// NewGitPublicKey is the public half of a Git key generated in this run, or
// empty. init shows it once the config is written, to add to the git host.
func (wiz Model) NewGitPublicKey() string {
	return wiz.gitPublicKey
}

// MintsKeys reports whether create will mint auth keys itself, through the
// OAuth client, rather than ask for one.
func (wiz Model) MintsKeys() bool {
	return wiz.oauthID != "" && wiz.oauthSecret != ""
}

// onNotice takes a background result that only changes what is shown.
func (wiz Model) onNotice(msg tea.Msg) Model {
	switch msg := msg.(type) {
	case browserOpened:
		return wiz.onBrowser(msg)
	case copied:
		return wiz.onCopied(msg)
	case updateFound:
		wiz.update = msg
	case clientSaved:
		return wiz.onClientSaved(msg)
	}

	return wiz
}

func (wiz Model) onTick(msg spinner.TickMsg) (tea.Model, tea.Cmd) {
	if wiz.step != stepLoad && !wiz.checking {
		return wiz, nil
	}

	if wiz.step == stepLoad {
		wiz.drift++
		if wiz.drift == minLoadTicks && wiz.loaded && !wiz.seedPending {
			return wiz.leaveLoad()
		}
	}

	var cmd tea.Cmd

	wiz.spin, cmd = wiz.spin.Update(msg)

	return wiz, cmd
}

func (wiz Model) onReady(msg catalogReady) (tea.Model, tea.Cmd) {
	if wiz.quit {
		return wiz, nil
	}

	wiz.cat = msg.cat
	if len(wiz.cat.Locations) == 0 || len(wiz.cat.Machines) == 0 {
		wiz.fatal = "Could not read Hetzner locations and prices.\nThe API returned none."

		return wiz, tea.Quit
	}

	wiz.cat.Images = ensureImage(wiz.cat.Images, wiz.image)
	wiz.image = preferredImage(wiz.cat, wiz.image)
	wiz.location = preferredLocation(wiz.cat, wiz.location)
	wiz.suggestImage = wiz.image
	wiz.suggestLocation = wiz.location
	wiz = wiz.settleMachine()
	wiz.loaded = true

	return wiz.leaveLoad()
}

// onSeedChecked settles the OAuth client from the config. One that still
// works means the tailnet is set up, so the hub shows that section as done.
// One that does not is dropped, so the tailnet steps make a new one.
func (wiz Model) onSeedChecked(msg seedChecked) (Model, tea.Cmd) {
	wiz.seedPending = false

	if msg.err == nil {
		wiz.tailnetDecided = true

		return wiz.leaveLoad()
	}

	wiz.oauthID = ""
	wiz.oauthSecret = ""
	wiz.note = strings.Join(strings.Fields(msg.err.Error()), " ") + " A new OAuth client is made here."

	return wiz.leaveLoad()
}

// leaveLoad moves off the loading screen once both the catalog and the
// client check are in, whichever came last, to the hub. A first run stays:
// its welcome waits for enter, and begin takes it to the first section. A
// later run starts where something is still needed, or at the top: it has a
// reason, and the reason is a section, never Write.
func (wiz Model) leaveLoad() (Model, tea.Cmd) {
	if wiz.quit || !wiz.loaded || wiz.seedPending || wiz.drift < minLoadTicks || wiz.guided {
		return wiz, nil
	}

	choice := wiz.hubCursor()
	if choice == hubWrite {
		choice = hubBox
	}

	// The note about a dropped OAuth client is for the hub to show.
	note := wiz.note
	wiz.saved = wiz.Config()
	wiz.begun = true
	next, cmd := wiz.openHub(choice)
	next.note = note

	return next, cmd
}

// onResult ends a background call: the check of the client from the config,
// the OAuth client check, the policy read, or the policy write. A failure
// keeps the step, with the API's reason, so it can be fixed and tried again.
func (wiz Model) onResult(msg tea.Msg) (Model, tea.Cmd) {
	switch result := msg.(type) {
	case seedChecked:
		return wiz.onSeedChecked(result)
	case clientChecked:
		return wiz.settleCheck(result)
	case setupPlanned:
		return wiz.settlePlan(result)
	case setupApplied:
		return wiz.settleApply(result)
	default:
		return wiz, nil
	}
}

func (wiz Model) settleCheck(msg clientChecked) (Model, tea.Cmd) {
	wiz.checking = false

	if msg.err != nil {
		wiz.note = strings.Join(strings.Fields(msg.err.Error()), " ")

		return wiz, nil
	}

	return wiz.keepClient(wiz.advance())
}

func (wiz Model) onBrowser(msg browserOpened) Model {
	wiz.note = "Opened " + msg.url + " in your browser."
	if msg.err != nil {
		wiz.note = "Could not open a browser. Visit " + msg.url
	}

	return wiz
}

func (wiz Model) onCopied(msg copied) Model {
	wiz.note = msg.done
	if !msg.ok {
		wiz.note = msg.failed
	}

	return wiz
}

func (wiz Model) onKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	if wiz.askingQuit {
		return wiz.answerQuit(msg)
	}

	if wiz.quitKey(msg) {
		return wiz.requestQuit()
	}

	if wiz.step == stepLoad {
		return wiz.onLoadKey(msg)
	}

	if wiz.checking {
		return wiz, nil
	}

	if msg.String() == "esc" {
		return wiz.back()
	}

	if wiz.step == stepHub {
		return wiz.onHub(msg)
	}

	if wiz.typing() {
		return wiz.onText(msg)
	}

	return wiz.onList(msg)
}

// onLoadKey takes a key on the loading screen or the welcome: it cuts the
// logo's minimum short once there is something to show, enter begins a
// first run, and u copies the update command when there is one.
func (wiz Model) onLoadKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	if msg.String() == "u" && wiz.update.latest != "" {
		return wiz, copyUpdate()
	}

	wiz.drift = max(wiz.drift, minLoadTicks)

	if !wiz.guided {
		return wiz.leaveLoad()
	}

	if msg.String() == keyEnter && wiz.ready() {
		return wiz.begin()
	}

	return wiz, nil
}

func (wiz Model) quitKey(msg tea.KeyMsg) bool {
	if msg.String() == "ctrl+c" {
		return true
	}

	return msg.String() == "q" && !wiz.typing()
}

func (wiz Model) onText(msg tea.KeyMsg) (Model, tea.Cmd) {
	if wiz.step == stepGitIdentity {
		return wiz.onIdentity(msg)
	}

	// The field takes every printable key, so the page opens with ctrl+o
	// rather than the o the list screens use.
	if page := wiz.stepPage(); msg.String() == keyOpenPage && page != "" {
		return wiz, openBrowser(page)
	}

	if msg.String() != keyEnter {
		var cmd tea.Cmd

		wiz.input, cmd = wiz.input.Update(msg)
		wiz.note = ""

		return wiz, cmd
	}

	next, problem := wiz.commitText(wiz.input.Value())
	if problem != "" {
		wiz.note = problem

		return wiz, nil
	}

	switch next.step {
	case stepToken:
		return next.startPlan()
	case stepOAuthSecret:
		return next.startCheck()
	case stepLoad, stepHub, stepLocation, stepMachine, stepImage, stepGitKey, stepGitIdentity, stepTools,
		stepAccess, stepPreview, stepPolicy, stepKeys, stepOAuthID:
		return next.advance()
	default:
		return next.advance()
	}
}

// startPlan reads the policy with the access token and works out what it
// lacks, before anything is shown or written.
func (wiz Model) startPlan() (Model, tea.Cmd) {
	wiz.checking = true
	wiz.busy = "Reading the tailnet policy"
	wiz.note = ""

	return wiz, tea.Batch(wiz.spin.Tick, planSetup(tailscale.AccessToken{Secret: wiz.token}, wiz.tag))
}

func planSetup(token tailscale.AccessToken, tag string) tea.Cmd {
	return func() tea.Msg {
		policy, err := token.ReadPolicy(context.Background())
		if err != nil {
			return setupPlanned{err: err}
		}

		plan, err := tailscale.PlanSetup(policy, tag)

		return setupPlanned{policy: policy, plan: plan, err: err}
	}
}

func (wiz Model) settlePlan(msg setupPlanned) (Model, tea.Cmd) {
	wiz.checking = false

	if msg.err != nil {
		wiz.note = strings.Join(strings.Fields(msg.err.Error()), " ")

		return wiz, nil
	}

	wiz.policy = msg.policy
	wiz.plan = msg.plan

	return wiz.advance()
}

// startApply writes the planned changes and creates the OAuth client. A run
// that already did so just moves on, so going back never applies twice.
func (wiz Model) startApply() (Model, tea.Cmd) {
	if wiz.setUp {
		return wiz.advance()
	}

	wiz.checking = true
	wiz.busy = "Updating the policy, checking your login, and creating the OAuth client"
	wiz.note = ""

	token := tailscale.AccessToken{Secret: wiz.token}
	existing := tailscale.OAuthClient{ID: wiz.oauthID, Secret: wiz.oauthSecret}

	apply := applySetup(token, existing, wiz.policy, wiz.plan, wiz.narrow, wiz.tag, wiz.tailnet.Login)

	return wiz, tea.Batch(wiz.spin.Tick, apply)
}

func applySetup(
	token tailscale.AccessToken,
	existing tailscale.OAuthClient,
	policy tailscale.Policy,
	plan tailscale.SetupPlan,
	narrow bool,
	tag, login string,
) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()

		written, err := token.ApplySetup(ctx, policy, plan, narrow, login)
		if err != nil {
			return setupApplied{err: err}
		}

		client := existing
		if !client.Configured() {
			client, err = token.CreateClient(ctx, tag)
			if err != nil {
				return setupApplied{err: err}
			}
		}

		err = client.Check(ctx, tag)
		if err != nil {
			return setupApplied{err: err}
		}

		// Re-plan on the written policy, so the preview shows everything in
		// place should the user step back to it.
		plan, err = tailscale.PlanSetup(written, tag)

		return setupApplied{client: client, plan: plan, err: err}
	}
}

func (wiz Model) settleApply(msg setupApplied) (Model, tea.Cmd) {
	wiz.checking = false

	if msg.err != nil {
		wiz.note = strings.Join(strings.Fields(msg.err.Error()), " ")

		return wiz, nil
	}

	wiz.oauthID = msg.client.ID
	wiz.oauthSecret = msg.client.Secret
	wiz.plan = msg.plan
	wiz.token = ""
	wiz.setUp = true

	return wiz.keepClient(wiz.advance())
}

// startCheck tries the OAuth client before the form moves on, so a wrong
// scope or tag is found here rather than at the first create.
func (wiz Model) startCheck() (Model, tea.Cmd) {
	wiz.checking = true
	wiz.busy = checkingNote
	wiz.note = ""

	client := tailscale.OAuthClient{ID: wiz.oauthID, Secret: wiz.oauthSecret}

	return wiz, tea.Batch(wiz.spin.Tick, checkClient(client, wiz.tag))
}

func checkClient(client tailscale.OAuthClient, tag string) tea.Cmd {
	return func() tea.Msg {
		return clientChecked{err: client.Check(context.Background(), tag)}
	}
}

func checkSeed(client tailscale.OAuthClient, tag string) tea.Cmd {
	return func() tea.Msg {
		return seedChecked{err: client.Check(context.Background(), tag)}
	}
}

func (wiz Model) onList(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch wiz.step {
	case stepPreview:
		return wiz.onPreview(msg)
	case stepPolicy:
		return wiz.onPolicy(msg)
	case stepKeys:
		return wiz.onKeysList(msg)
	case stepGitKey:
		return wiz.onGitList(msg)
	case stepTools:
		return wiz.onTools(msg)
	case stepLocation:
		return wiz.onLocations(msg)
	case stepLoad, stepHub, stepMachine, stepImage, stepGitIdentity, stepAccess, stepToken, stepOAuthID,
		stepOAuthSecret:
		return wiz.onPicker(msg)
	default:
		return wiz.onPicker(msg)
	}
}

// onLocations moves west and east on the map with ← and →, round the world
// past either end, and takes the list's keys otherwise.
func (wiz Model) onLocations(msg tea.KeyMsg) (Model, tea.Cmd) {
	step := map[string]int{"left": -1, "h": -1, "right": 1, "l": 1}[msg.String()]
	if step == 0 {
		return wiz.onPicker(msg)
	}

	wiz.note = ""

	return wiz.selectID(nextLocation(wiz.cat.Locations, wiz.location, step)), nil
}

// onPreview applies on enter. Space flips the narrowing of the allow-all
// rule, which is the one change that can touch other tagged devices.
func (wiz Model) onPreview(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case keyEnter:
		return wiz.startApply()
	case " ":
		if wiz.plan.StockRule != tailscale.NoStockRule && !wiz.setUp {
			wiz.narrow = !wiz.narrow
		}

		return wiz, nil
	default:
		return wiz, nil
	}
}

func (wiz Model) onPolicy(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case keyEnter:
		return wiz.advance()
	case keyOpen:
		return wiz, openBrowser(tailscale.PolicyURL)
	case keyCopy:
		return wiz, copyText(wiz.policyText(), "The three lines are on the clipboard.",
			"No clipboard tool found. Copy the lines by hand.")
	default:
		return wiz, nil
	}
}

func (wiz Model) onKeysList(msg tea.KeyMsg) (Model, tea.Cmd) {
	if msg.String() == keyEnter {
		return wiz.confirmKeys()
	}

	return wiz.onPicker(msg)
}

// confirmKeys leaves the OAuth client behind when the user would rather be
// asked, so a secret never stays in the file by accident.
func (wiz Model) confirmKeys() (Model, tea.Cmd) {
	if wiz.keysChoice != keysChoiceMint {
		wiz.oauthID = ""
		wiz.oauthSecret = ""
	}

	return wiz.advance()
}

func (wiz Model) onTools(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case " ":
		wiz.tools = config.ToggleTool(wiz.tools, wiz.toolChoice)
		wiz.note = ""

		return wiz, nil
	case keyEnter:
		wiz.tools = config.StoredTools(wiz.tools)

		return wiz.advance()
	default:
		return wiz.onPicker(msg)
	}
}

func (wiz Model) onGitList(msg tea.KeyMsg) (Model, tea.Cmd) {
	if msg.String() == keyEnter {
		return wiz.confirmGit()
	}

	return wiz.onPicker(msg)
}

func (wiz Model) onPicker(msg tea.KeyMsg) (Model, tea.Cmd) {
	rows := wiz.rows()
	if msg.String() == keyEnter {
		return wiz.confirmRow(rows)
	}

	if msg.String() == "a" {
		return wiz.cycleArch(), nil
	}

	if msg.String() == "c" {
		return wiz.cycleCPU(), nil
	}

	moved, ok := listDelta(msg.String(), wiz.pageDelta())
	if !ok {
		return wiz, nil
	}

	wiz.note = ""
	if moved == listHome {
		return wiz.selectID(firstID(rows)), nil
	}

	if moved == listEnd {
		return wiz.selectID(lastID(rows)), nil
	}

	return wiz.selectID(nextRow(rows, wiz.selectedID(), moved)), nil
}

func listDelta(key string, page int) (int, bool) {
	switch key {
	case "up", "k":
		return -1, true
	case "down", "j":
		return 1, true
	case "pgup":
		return -page, true
	case "pgdown":
		return page, true
	case "home", "g":
		return listHome, true
	case "end", "G":
		return listEnd, true
	default:
		return 0, false
	}
}

func (wiz Model) cycleArch() Model {
	if wiz.step != stepMachine {
		return wiz
	}

	wiz.archFilter = cycle(wiz.archFilter, []string{"", "x86", "arm"})
	wiz.note = ""

	return wiz.ensureSelection()
}

func (wiz Model) cycleCPU() Model {
	if wiz.step != stepMachine {
		return wiz
	}

	wiz.cpuFilter = cycle(wiz.cpuFilter, []string{"", "shared", "dedicated"})
	wiz.note = ""

	return wiz.ensureSelection()
}

func (wiz Model) confirmRow(rows []pickerRow) (Model, tea.Cmd) {
	row, ok := findRow(rows, wiz.selectedID())
	if !ok {
		wiz.note = "Nothing to choose here."

		return wiz, nil
	}

	if wiz.step == stepMachine && row.dim {
		wiz.note = row.id + " is sold out in " + wiz.placeName() + "."

		return wiz, nil
	}

	wiz = wiz.selectID(row.id)
	if wiz.step == stepLocation {
		wiz = wiz.settleMachine()
	}

	return wiz.advance()
}

// advance moves to the next step of the section, or back to the hub after
// its last one.
func (wiz Model) advance() (Model, tea.Cmd) {
	wiz.note = ""
	wiz.gitEditing = false

	sec, ok := sectionOf(wiz.step)
	if !ok {
		return wiz, nil
	}

	steps := wiz.sectionSteps(sec)

	index := slices.Index(steps, wiz.step)
	if index < 0 || index+1 >= len(steps) {
		return wiz.finishSection(sec)
	}

	wiz.step = steps[index+1]

	return wiz.prepareStep()
}

// back moves to the previous step of the section, or to the hub from its
// first one. On the hub it quits, and nothing is written.
func (wiz Model) back() (Model, tea.Cmd) {
	if wiz.step == stepGitKey && wiz.gitEditing {
		wiz.gitEditing = false
		wiz.note = ""

		return wiz.openGitChoice(false)
	}

	sec, ok := sectionOf(wiz.step)
	if !ok {
		return wiz.requestQuit()
	}

	steps := wiz.sectionSteps(sec)

	index := slices.Index(steps, wiz.step)
	if index <= 0 && wiz.guided {
		return wiz.guidedBack(sec)
	}

	if index <= 0 {
		return wiz.openHub(sectionRow(sec))
	}

	wiz.step = steps[index-1]
	wiz.note = ""

	return wiz.prepareStep()
}

func (wiz Model) prepareStep() (Model, tea.Cmd) {
	if wiz.step == stepGitKey && !wiz.gitEditing {
		return wiz.openGitChoice(true)
	}

	if wiz.step == stepGitIdentity {
		return wiz.prepareIdentity()
	}

	if wiz.step == stepTools && !config.KnownTool(wiz.toolChoice) {
		wiz.toolChoice = config.ToolOpenCode
	}

	if !wiz.typing() {
		wiz.input.Blur()

		return wiz, nil
	}

	wiz.input.EchoMode = textinput.EchoNormal
	if wiz.step == stepOAuthSecret || wiz.step == stepToken {
		wiz.input.EchoMode = textinput.EchoPassword
	}

	wiz.input.SetValue(wiz.fieldValue())
	wiz.input.Placeholder = wiz.fieldPlaceholder()
	wiz.input.CursorEnd()
	wiz.input.Width = wiz.fieldWidth()

	return wiz, wiz.input.Focus()
}

func (wiz Model) typing() bool {
	switch wiz.step {
	case stepGitKey:
		return wiz.gitEditing
	case stepToken, stepOAuthID, stepOAuthSecret, stepGitIdentity:
		return true
	case stepImage:
		return len(wiz.cat.Images) == 0
	case stepLoad, stepHub, stepAccess, stepPreview, stepPolicy, stepKeys, stepTools, stepLocation, stepMachine:
		return false
	default:
		return false
	}
}

func (wiz Model) fieldWidth() int {
	width := wiz.width - fieldChrome
	if width < fieldWidthMin {
		return fieldWidthMin
	}

	if width > fieldWidthMax {
		return fieldWidthMax
	}

	return width
}

func (wiz Model) fieldValue() string {
	values := map[stepID]string{
		stepToken:       wiz.token,
		stepOAuthID:     wiz.oauthID,
		stepOAuthSecret: wiz.oauthSecret,
		stepGitKey:      wiz.gitKey,
		stepGitIdentity: wiz.gitName,
		stepImage:       wiz.image,
	}

	return values[wiz.step]
}

func (wiz Model) fieldPlaceholder() string {
	placeholders := map[stepID]string{ //nolint:gosec // G101: placeholder text, not a secret.
		stepToken:       "tskey-api-...",
		stepOAuthID:     "k1234567CNTRL",
		stepOAuthSecret: "tskey-client-...",
		stepGitKey:      "$HOME/.ssh/github_ed25519",
		stepGitIdentity: namePlaceholder,
		stepImage:       config.DefaultImage,
	}

	return placeholders[wiz.step]
}

func (wiz Model) commitText(value string) (Model, string) {
	value = strings.TrimSpace(value)

	switch wiz.step {
	case stepGitKey:
		return wiz.commitPath(value)
	case stepToken, stepOAuthID, stepOAuthSecret, stepImage:
		return wiz.commitPlain(value)
	case stepLoad, stepHub, stepAccess, stepPreview, stepPolicy, stepKeys, stepGitIdentity, stepTools, stepLocation,
		stepMachine:
		return wiz, ""
	default:
		return wiz, ""
	}
}

func (wiz Model) commitPlain(value string) (Model, string) {
	switch wiz.step {
	case stepToken:
		return wiz.commitChecked(value, func(stored string) string {
			return config.InvalidToken("Access token", stored)
		}, func(next Model, stored string) Model {
			next.token = stored

			return next
		})
	case stepOAuthID:
		return wiz.commitChecked(value, func(stored string) string {
			return config.InvalidToken("Client id", stored)
		}, func(next Model, stored string) Model {
			next.oauthID = stored

			return next
		})
	case stepOAuthSecret:
		return wiz.commitChecked(value, func(stored string) string {
			return config.InvalidToken("Client secret", stored)
		}, func(next Model, stored string) Model {
			next.oauthSecret = stored

			return next
		})
	case stepImage:
		return wiz.commitNamed(value)
	case stepLoad, stepHub, stepAccess, stepPreview, stepPolicy, stepKeys, stepGitKey, stepGitIdentity, stepTools,
		stepLocation, stepMachine:
		return wiz, ""
	default:
		return wiz, ""
	}
}

func (wiz Model) commitNamed(value string) (Model, string) {
	switch wiz.step {
	case stepImage:
		return wiz.commitChecked(value, config.InvalidImage, func(next Model, stored string) Model {
			next.image = stored

			return next
		})
	case stepLoad, stepHub, stepAccess, stepToken, stepPreview, stepPolicy, stepKeys, stepOAuthID, stepOAuthSecret,
		stepGitKey, stepGitIdentity, stepTools, stepLocation, stepMachine:
		return wiz, ""
	default:
		return wiz, ""
	}
}

func (wiz Model) commitChecked(
	value string,
	check func(string) string,
	store func(Model, string) Model,
) (Model, string) {
	problem := check(value)
	if problem != "" {
		return wiz, problem
	}

	return store(wiz, value), ""
}

func (wiz Model) commitPath(value string) (Model, string) {
	problem := config.InvalidPrivateKey(value)
	if problem != "" {
		return wiz, problem
	}

	stored, problem := config.CanonicalPath(value)
	if problem != "" {
		return wiz, problem
	}

	wiz.gitKey = stored

	return wiz, ""
}

func (wiz Model) openGitChoice(reset bool) (Model, tea.Cmd) {
	wiz.gitEditing = false
	wiz.gitSuggest = gitChoiceNew

	if config.InvalidPrivateKey(wiz.gitKey) == "" {
		wiz.gitSuggest = gitChoiceUse
	}

	if reset || wiz.gitChoice == "" {
		wiz.gitChoice = wiz.gitSuggest
	}

	wiz.input.Blur()

	return wiz, nil
}

func (wiz Model) editGitPath() (Model, tea.Cmd) {
	wiz.gitEditing = true
	wiz.note = ""

	return wiz.prepareStep()
}

func (wiz Model) confirmGit() (Model, tea.Cmd) {
	switch wiz.gitChoice {
	case gitChoiceUse:
		if config.InvalidPrivateKey(wiz.gitKey) == "" {
			return wiz.advance()
		}

		return wiz.editGitPath()
	case gitChoiceNew:
		return wiz.generateGit()
	case gitChoicePath:
		return wiz.editGitPath()
	default:
		return wiz, nil
	}
}

func (wiz Model) generateGit() (Model, tea.Cmd) {
	private := gitKeyTarget()
	if private == "" {
		wiz.note = displayKeyPath(GitKeyPath) + " already exists."

		return wiz, nil
	}

	key, problem := generateGitKey(private)
	if problem != "" {
		wiz.note = problem

		return wiz, nil
	}

	wiz.gitKey = key.private
	wiz.gitPublicKey = key.public

	return wiz.advance()
}

func (wiz Model) settleMachine() Model {
	wiz.serverType = pickMachine(wiz.cat, wiz.location, wiz.serverType)
	wiz.suggestMachine = wiz.serverType

	return wiz
}

func (wiz Model) ensureSelection() Model {
	if _, ok := findRow(wiz.rows(), wiz.selectedID()); ok {
		return wiz
	}

	return wiz.selectID(firstID(wiz.rows()))
}

func (wiz Model) selectedID() string {
	ids := map[stepID]string{
		stepHub:      wiz.hubChoice,
		stepAccess:   wiz.accessChoice,
		stepKeys:     wiz.keysChoice,
		stepGitKey:   wiz.gitChoice,
		stepTools:    wiz.toolChoice,
		stepImage:    wiz.image,
		stepLocation: wiz.location,
		stepMachine:  wiz.serverType,
	}

	return ids[wiz.step]
}

func (wiz Model) selectID(rowID string) Model {
	if rowID == "" {
		return wiz
	}

	switch wiz.step {
	case stepHub:
		wiz.hubChoice = keepHubChoice(wiz.hubChoice, rowID)
	case stepAccess:
		wiz.accessChoice = keepAccessChoice(wiz.accessChoice, rowID)
	case stepKeys:
		wiz.keysChoice = keepKeysChoice(wiz.keysChoice, rowID)
	case stepGitKey:
		wiz.gitChoice = keepGitChoice(wiz.gitChoice, rowID)
	case stepTools:
		wiz.toolChoice = keepToolChoice(wiz.toolChoice, rowID)
	case stepImage, stepLocation, stepMachine:
		wiz = wiz.selectCatalog(rowID)
	case stepLoad, stepToken, stepPreview, stepPolicy, stepOAuthID, stepOAuthSecret, stepGitIdentity:
	}

	return wiz
}

// selectCatalog stores a Box section pick; the ids are the catalog's names.
func (wiz Model) selectCatalog(rowID string) Model {
	switch wiz.step {
	case stepImage:
		wiz.image = rowID
	case stepLocation:
		wiz.location = rowID
	case stepMachine:
		wiz.serverType = rowID
	case stepLoad, stepHub, stepGitKey, stepGitIdentity, stepTools, stepAccess, stepToken, stepPreview,
		stepPolicy, stepKeys, stepOAuthID, stepOAuthSecret:
	}

	return wiz
}

func keepAccessChoice(current, rowID string) string {
	if rowID == accessToken || rowID == accessManual {
		return rowID
	}

	return current
}

func keepKeysChoice(current, rowID string) string {
	if rowID == keysChoiceMint || rowID == keysChoiceAsk {
		return rowID
	}

	return current
}

func keepGitChoice(current, rowID string) string {
	if rowID == gitChoiceUse || rowID == gitChoiceNew || rowID == gitChoicePath {
		return rowID
	}

	return current
}

func keepToolChoice(current, rowID string) string {
	if config.KnownTool(rowID) {
		return rowID
	}

	return current
}

// progress names the section and the step within it, for the header. The
// hub has none.
func (wiz Model) progress() string {
	sec, ok := sectionOf(wiz.step)
	if !ok {
		return ""
	}

	steps := wiz.sectionSteps(sec)

	index := slices.Index(steps, wiz.step)
	if index < 0 {
		return sectionName(sec)
	}

	return fmt.Sprintf("%s · %d of %d", sectionName(sec), index+1, len(steps))
}

// ensureImage keeps a configured image the catalog no longer lists, such as
// a deprecated release Hetzner still boots, but never one buildbox cannot
// provision.
func ensureImage(offers []hetzner.ImageOffer, name string) []hetzner.ImageOffer {
	if name == "" || imageKnown(offers, name) || config.InvalidImage(name) != "" {
		return offers
	}

	extra := hetzner.ImageOffer{Name: name, Detail: "from the current config"}

	return append([]hetzner.ImageOffer{extra}, offers...)
}

func imageKnown(offers []hetzner.ImageOffer, name string) bool {
	for _, offer := range offers {
		if offer.Name == name {
			return true
		}
	}

	return false
}

func preferredImage(cat hetzner.Catalog, prefer string) string {
	if imageKnown(cat.Images, prefer) {
		return prefer
	}

	if imageKnown(cat.Images, config.DefaultImage) {
		return config.DefaultImage
	}

	if len(cat.Images) > 0 {
		return cat.Images[0].Name
	}

	if prefer != "" && config.InvalidImage(prefer) == "" {
		return prefer
	}

	return config.DefaultImage
}

func cycle(current string, options []string) string {
	for i, option := range options {
		if option == current {
			return options[(i+1)%len(options)]
		}
	}

	if len(options) == 0 {
		return ""
	}

	return options[0]
}

// LoadCatalog fetches the catalog in the background and hands the result to
// the form as a message.
func LoadCatalog(ctx context.Context, client *hcloud.Client) tea.Cmd {
	return func() tea.Msg {
		cat, err := hetzner.LoadCatalog(ctx, client)
		if err != nil {
			return catalogFailed{err: err}
		}

		return catalogReady{cat: cat}
	}
}
