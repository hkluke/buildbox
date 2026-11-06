package tailscale

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/tailscale/hujson"

	"github.com/hkluke/buildbox/internal/abort"
	"github.com/hkluke/buildbox/internal/config"
)

const (
	policyPath   = "/api/v2/tailnet/-/acl"
	validatePath = policyPath + "/validate"
	hujsonType   = "application/hujson"
	ownerGroup   = "autogroup:owner"
	memberGroup  = "autogroup:member"
	accessToken  = "access token"
	clientLabel  = "buildbox"

	opAdd            = "add"
	sectionTagOwners = "tagOwners"
	sectionACLs      = "acls"
	sectionGrants    = "grants"
	sectionSSH       = "ssh"
	sectionSSHTests  = "sshTests"
	rootUser         = "root"

	// NoStockRule means the policy has no allow-all rule to narrow.
	NoStockRule = -1
)

// AccessToken is an API access token the user generated for one init run.
// It acts with the user's own rights, so it can read and write the policy
// and create the OAuth client. buildbox uses it during init and never
// stores it.
type AccessToken struct {
	Secret string

	// baseURL replaces the API host in tests.
	baseURL string
}

// Policy is the tailnet policy as Tailscale serves it: HuJSON with the
// user's comments, and the ETag that guards the next write.
type Policy struct {
	Raw  []byte
	ETag string
}

// Change is one thing the policy needs for the boxes: the section, the line
// as shown to the user, and whether it is already there. A change with Adds
// amends a rule in place, giving it the named users, rather than adding a
// line.
type Change struct {
	Section string
	Line    string
	Present bool
	Adds    []string

	key   string
	value json.RawMessage
	index int
}

// SetupPlan is what init will do to the policy.
type SetupPlan struct {
	Changes []Change
	// StockRule is the index of the allow-all rule in acls or grants, or
	// NoStockRule. Narrowing its source to members is what keeps boxes
	// from reaching the rest of the tailnet.
	StockRule int
	// Grants means the policy uses the grants syntax rather than acls.
	Grants bool
	// Tag is the tag the plan is for, which the login test names.
	Tag string
}

// Pending reports whether any change is still missing.
func (plan SetupPlan) Pending() bool {
	for _, change := range plan.Changes {
		if !change.Present {
			return true
		}
	}

	return false
}

// ReadPolicy fetches the policy with its comments.
func (token AccessToken) ReadPolicy(ctx context.Context) (Policy, error) {
	var raw []byte

	headers, err := token.api().do(ctx, apiCall{
		method:     http.MethodGet,
		path:       policyPath,
		bearer:     token.Secret,
		accept:     hujsonType,
		credential: accessToken,
	}, &raw)
	if err != nil {
		return Policy{}, err
	}

	return Policy{Raw: raw, ETag: headers.Get("ETag")}, nil
}

// policyShape is the part of the policy the plan looks at. Everything else
// is left alone.
type policyShape struct {
	TagOwners map[string]json.RawMessage `json:"tagOwners"`
	ACLs      []policyRule               `json:"acls"`
	Grants    []policyRule               `json:"grants"`
	SSH       []policyRule               `json:"ssh"`
}

type policyRule struct {
	Action string   `json:"action"`
	Src    []string `json:"src"`
	Dst    []string `json:"dst"`
	IP     []string `json:"ip"`
	Users  []string `json:"users"`
}

// PlanSetup reads what the policy already has for tag and lists what is
// missing. It changes nothing.
func PlanSetup(policy Policy, tag string) (SetupPlan, error) {
	sections, shape, err := decodePolicy(policy.Raw)
	if err != nil {
		return SetupPlan{}, err
	}

	_, hasACLs := sections[sectionACLs]
	_, hasGrants := sections[sectionGrants]
	grants := hasGrants && !hasACLs

	_, ownerPresent := shape.TagOwners[tag]

	plan := SetupPlan{Grants: grants, StockRule: stockRule(shape, grants), Tag: tag}
	plan.Changes = []Change{
		{
			Section: sectionTagOwners,
			Line:    tagOwnerLine(tag),
			Present: ownerPresent,
			key:     tag,
			value:   json.RawMessage(`["` + ownerGroup + `"]`),
		},
		networkChange(shape, grants, tag),
		sshChange(shape.SSH, tag),
	}

	return plan, nil
}

// sshChange is the ssh rule: present when any rule for the tag names both
// box users, amended when a rule in buildbox's own shape lacks one, so that
// a user someone added by hand stays and no second rule appears, and added
// otherwise. Nothing is ever taken out of a rule.
func sshChange(rules []policyRule, tag string) Change {
	change := Change{Section: sectionSSH, Line: SSHRule(tag), value: ruleValue(SSHRule(tag)), index: -1}

	for _, rule := range rules {
		if slices.Contains(rule.Dst, tag) && hasUsers(rule, boxUsers()) {
			change.Present = true

			return change
		}
	}

	own := slices.IndexFunc(rules, func(rule policyRule) bool { return ownShape(rule, tag) })
	if own < 0 {
		return change
	}

	users := slices.Clone(rules[own].Users)

	for _, user := range boxUsers() {
		if !slices.Contains(users, user) {
			change.Adds = append(change.Adds, user)
			users = append(users, user)
		}
	}

	value, _ := json.Marshal(users) //nolint:errchkjson // strings always encode.

	change.index = own
	change.Line = sshRule(tag, users)
	change.value = value

	return change
}

func hasUsers(rule policyRule, users []string) bool {
	for _, user := range users {
		if !slices.Contains(rule.Users, user) {
			return false
		}
	}

	return true
}

// ownShape is the rule init writes: owners, to the tag, accept. A rule in
// any other shape was written by someone else and is left alone.
func ownShape(rule policyRule, tag string) bool {
	return rule.Action == "accept" && slices.Equal(rule.Src, []string{ownerGroup}) &&
		slices.Equal(rule.Dst, []string{tag})
}

func networkChange(shape policyShape, grants bool, tag string) Change {
	if grants {
		return Change{
			Section: sectionGrants,
			Line:    grantLine(tag),
			Present: slices.ContainsFunc(shape.Grants, func(rule policyRule) bool {
				return slices.Contains(rule.Src, ownerGroup) && slices.Contains(rule.Dst, tag)
			}),
			value: ruleValue(grantLine(tag)),
		}
	}

	return Change{
		Section: sectionACLs,
		Line:    aclLine(tag),
		Present: slices.ContainsFunc(shape.ACLs, func(rule policyRule) bool {
			return slices.Contains(rule.Src, ownerGroup) && slices.Contains(rule.Dst, tag+":*")
		}),
		value: ruleValue(aclLine(tag)),
	}
}

// stockRule finds Tailscale's default allow-all rule.
func stockRule(shape policyShape, grants bool) int {
	if grants {
		return slices.IndexFunc(shape.Grants, func(rule policyRule) bool {
			return onlyStar(rule.Src) && onlyStar(rule.Dst) && onlyStar(rule.IP)
		})
	}

	return slices.IndexFunc(shape.ACLs, func(rule policyRule) bool {
		return rule.Action == "accept" && onlyStar(rule.Src) && slices.Equal(rule.Dst, []string{"*:*"})
	})
}

func onlyStar(values []string) bool {
	return slices.Equal(values, []string{"*"})
}

// ruleValue is a displayed line as a JSON value: without its trailing comma.
func ruleValue(line string) json.RawMessage {
	return json.RawMessage(strings.TrimSuffix(line, ","))
}

// decodePolicy reads the HuJSON policy as plain JSON, for inspection only.
// Standardize works on the bytes it is given, so it gets a copy: the raw
// policy, comments included, is what gets patched later.
func decodePolicy(raw []byte) (map[string]json.RawMessage, policyShape, error) {
	standard, err := hujson.Standardize(bytes.Clone(raw))
	if err != nil {
		return nil, policyShape{}, abort.Newf("Could not parse the tailnet policy.\n%s", err.Error())
	}

	var sections map[string]json.RawMessage

	err = json.Unmarshal(standard, &sections)
	if err != nil {
		return nil, policyShape{}, abort.Newf("Could not parse the tailnet policy.\n%s", err.Error())
	}

	var shape policyShape

	err = json.Unmarshal(standard, &shape)
	if err != nil {
		return nil, policyShape{}, abort.Newf("Could not read the tailnet policy's rules.\n%s", err.Error())
	}

	return sections, shape, nil
}

// ApplySetup adds the missing changes to the policy, narrows the allow-all
// rule when asked, has Tailscale validate the result and prove that login
// can SSH to the tag's boxes, and writes it. The write is refused if the
// policy changed since it was read. A plan with nothing to do is still
// checked, and returns the policy as it was.
func (token AccessToken) ApplySetup(
	ctx context.Context,
	policy Policy,
	plan SetupPlan,
	narrow bool,
	login string,
) (Policy, error) {
	patch, err := setupPatch(policy.Raw, plan, narrow)
	if err != nil {
		return Policy{}, err
	}

	updated := policy.Raw
	if patch != nil {
		updated, err = patchPolicy(policy.Raw, patch)
		if err != nil {
			return Policy{}, err
		}
	}

	err = token.check(ctx, updated, plan.Tag, login)
	if err != nil {
		return Policy{}, err
	}

	if patch == nil {
		return policy, nil
	}

	return token.write(ctx, policy.ETag, updated)
}

type patchOp struct {
	Op    string          `json:"op"`
	Path  string          `json:"path"`
	Value json.RawMessage `json:"value,omitempty"`
}

// setupPatch is the RFC 6902 patch for the plan, or nil when there is
// nothing to do. A missing section is created with its first entry.
func setupPatch(raw []byte, plan SetupPlan, narrow bool) ([]byte, error) {
	sections, _, err := decodePolicy(raw)
	if err != nil {
		return nil, err
	}

	ops := []patchOp{}

	for _, change := range plan.Changes {
		if op, needed := changeOp(sections, change); needed {
			ops = append(ops, op)
		}
	}

	if narrow && plan.StockRule != NoStockRule {
		section := sectionACLs
		if plan.Grants {
			section = sectionGrants
		}

		ops = append(ops, patchOp{
			Op:    "replace",
			Path:  fmt.Sprintf("/%s/%d/src", section, plan.StockRule),
			Value: json.RawMessage(`["` + memberGroup + `"]`),
		})
	}

	if len(ops) == 0 {
		return nil, nil
	}

	patch, err := json.Marshal(ops)
	if err != nil {
		return nil, fmt.Errorf("encode the policy patch: %w", err)
	}

	return patch, nil
}

// changeOp is the patch for one change, or none when it is present: an
// amended rule gets its users replaced, a missing line is added.
func changeOp(sections map[string]json.RawMessage, change Change) (patchOp, bool) {
	switch {
	case change.Present:
		return patchOp{}, false
	case len(change.Adds) > 0:
		return patchOp{
			Op:    "replace",
			Path:  fmt.Sprintf("/%s/%d/users", change.Section, change.index),
			Value: change.value,
		}, true
	default:
		return addOp(sections, change), true
	}
}

func addOp(sections map[string]json.RawMessage, change Change) patchOp {
	_, exists := sections[change.Section]

	if change.key != "" {
		if exists {
			return patchOp{Op: opAdd, Path: "/" + change.Section + "/" + escapePointer(change.key), Value: change.value}
		}

		member, _ := json.Marshal(change.key) //nolint:errchkjson // a string always encodes.
		object := "{" + string(member) + ": " + string(change.value) + "}"

		return patchOp{Op: opAdd, Path: "/" + change.Section, Value: json.RawMessage(object)}
	}

	if exists {
		return patchOp{Op: opAdd, Path: "/" + change.Section + "/-", Value: change.value}
	}

	return patchOp{Op: opAdd, Path: "/" + change.Section, Value: json.RawMessage("[" + string(change.value) + "]")}
}

// escapePointer applies RFC 6901 to one path segment.
func escapePointer(segment string) string {
	return strings.NewReplacer("~", "~0", "/", "~1").Replace(segment)
}

// patchPolicy applies the patch and keeps the user's comments. The result is
// formatted the way Tailscale's own tooling formats policies.
func patchPolicy(raw, patch []byte) ([]byte, error) {
	value, err := hujson.Parse(raw)
	if err != nil {
		return nil, abort.Newf("Could not parse the tailnet policy.\n%s", err.Error())
	}

	err = value.Patch(patch)
	if err != nil {
		return nil, abort.Newf("Could not change the tailnet policy.\n%s", err.Error())
	}

	value.Format()

	return value.Pack(), nil
}

type clientRequest struct {
	KeyType     string   `json:"keyType"`
	Scopes      []string `json:"scopes"`
	Tags        []string `json:"tags"`
	Description string   `json:"description"`
}

// CreateClient makes the OAuth client create will mint keys with: the
// auth_keys scope and the tag, nothing else. The tag must already be in the
// policy's tagOwners.
func (token AccessToken) CreateClient(ctx context.Context, tag string) (OAuthClient, error) {
	body, err := json.Marshal(clientRequest{
		KeyType:     "client",
		Scopes:      []string{"auth_keys"},
		Tags:        []string{tag},
		Description: clientLabel,
	})
	if err != nil {
		return OAuthClient{}, fmt.Errorf("encode the client request: %w", err)
	}

	var created struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}

	_, err = token.api().do(ctx, apiCall{
		method:      http.MethodPost,
		path:        keysPath,
		bearer:      token.Secret,
		contentType: jsonType,
		body:        bytes.NewReader(body),
		tag:         tag,
		credential:  accessToken,
	}, &created)
	if err != nil {
		return OAuthClient{}, err
	}

	if created.ID == "" || created.Key == "" {
		return OAuthClient{}, abort.New("The Tailscale API returned an OAuth client without its id or secret.")
	}

	return OAuthClient{ID: created.ID, Secret: created.Key, baseURL: token.baseURL}, nil
}

// check has Tailscale validate the policy. With a login, the copy it sends
// carries one ssh test, so the answer also says whether that login could
// reach the tag's boxes as agent and admin, and not as root. The test is
// never written: Tailscale runs stored tests on every later policy change.
func (token AccessToken) check(ctx context.Context, updated []byte, tag, login string) error {
	candidate := updated

	if login != "" {
		var err error

		candidate, err = withLoginTest(updated, tag, login)
		if err != nil {
			return err
		}
	}

	return token.validate(ctx, candidate, login)
}

// sshTest is one entry of the policy's sshTests section.
type sshTest struct {
	Src    string   `json:"src"`
	Dst    []string `json:"dst"`
	Accept []string `json:"accept"`
	Deny   []string `json:"deny"`
}

func withLoginTest(raw []byte, tag, login string) ([]byte, error) {
	sections, _, err := decodePolicy(raw)
	if err != nil {
		return nil, err
	}

	test, err := json.Marshal(sshTest{
		Src:    login,
		Dst:    []string{tag},
		Accept: []string{config.AgentUser, config.AdminUser},
		Deny:   []string{rootUser},
	})
	if err != nil {
		return nil, fmt.Errorf("encode the ssh test: %w", err)
	}

	patch, err := json.Marshal([]patchOp{addOp(sections, Change{Section: sectionSSHTests, value: test})})
	if err != nil {
		return nil, fmt.Errorf("encode the ssh test patch: %w", err)
	}

	return patchPolicy(raw, patch)
}

// write replaces the policy, refusing when it changed since it was read.
func (token AccessToken) write(ctx context.Context, etag string, updated []byte) (Policy, error) {
	var written []byte

	headers, err := token.api().do(ctx, apiCall{
		method:      http.MethodPost,
		path:        policyPath,
		bearer:      token.Secret,
		contentType: hujsonType,
		accept:      hujsonType,
		ifMatch:     etag,
		body:        bytes.NewReader(updated),
		credential:  accessToken,
	}, &written)
	if err != nil {
		return Policy{}, err
	}

	return Policy{Raw: written, ETag: headers.Get("ETag")}, nil
}

// validation is the validate endpoint's answer: nothing on success, or a
// message with the tests that failed or the warnings it found.
type validation struct {
	Message string `json:"message"`
	Data    []struct {
		User     string   `json:"user"`
		Errors   []string `json:"errors"`
		Warnings []string `json:"warnings"`
	} `json:"data"`
}

// passed is true when nothing failed. Warnings alone, such as a group that
// is not syncing, do not stop a write.
func (result validation) passed() bool {
	if result.Message == "" {
		return true
	}

	for _, item := range result.Data {
		if len(item.Errors) > 0 {
			return false
		}
	}

	return len(result.Data) > 0
}

// errorsFor are the failures of the tests run as login.
func (result validation) errorsFor(login string) []string {
	failures := []string{}

	for _, item := range result.Data {
		if login != "" && item.User == login {
			failures = append(failures, item.Errors...)
		}
	}

	return failures
}

func (result validation) detail() string {
	lines := []string{result.Message}

	for _, item := range result.Data {
		for _, failure := range item.Errors {
			lines = append(lines, item.User+": "+failure)
		}
	}

	return strings.Join(lines, "\n")
}

// validate asks Tailscale whether the new policy parses and passes the
// tests in it, including the one that proves login. Nothing is written on
// a failure.
func (token AccessToken) validate(ctx context.Context, policy []byte, login string) error {
	var answer []byte

	_, err := token.api().do(ctx, apiCall{
		method:      http.MethodPost,
		path:        validatePath,
		bearer:      token.Secret,
		contentType: hujsonType,
		body:        bytes.NewReader(policy),
		credential:  accessToken,
	}, &answer)
	if err != nil {
		return err
	}

	result := decodeValidation(answer)
	if result.passed() {
		return nil
	}

	if failures := result.errorsFor(login); len(failures) > 0 {
		return abort.Hint(
			"The policy would not let "+login+" log in to boxes",
			strings.Join(failures, "\n"),
			"Nothing was written. The ssh rule grants autogroup:owner; a login that is not the tailnet's "+
				"owner has to be named in the rule by hand:\n"+abort.Indent+PolicyURL,
		)
	}

	return abort.Hint(
		"Tailscale rejected the new policy",
		result.detail(),
		"Nothing was written. The policy can be fixed in the admin console, or set up by hand:\n"+
			abort.Indent+PolicyURL,
	)
}

// decodeValidation reads the answer; an empty body passes, and a body that
// is not JSON is reported by its first line.
func decodeValidation(answer []byte) validation {
	var result validation

	if len(bytes.TrimSpace(answer)) == 0 {
		return result
	}

	err := json.Unmarshal(answer, &result)
	if err != nil {
		result.Message = apiMessage(answer)
	}

	return result
}

func (token AccessToken) api() api {
	return api{base: token.baseURL}
}
