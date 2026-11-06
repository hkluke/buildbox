package tailscale

import (
	"context"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/hkluke/buildbox/internal/config"
)

// testAccess stands in for an API access token.
const testAccess = "tskey-api-test-token"

const testETag = "etag-1"

// testLogin is the user running init, whose login the ssh test proves.
const testLogin = "ada@example.com"

// stockPolicy is Tailscale's default policy for a new tailnet, comments and all.
const stockPolicy = `// Example/default ACLs for unrestricted connections.
{
	// Define the tags which can be applied to devices and by which users.
	// "tagOwners": {
	//  	"tag:example": ["autogroup:admin"],
	// },

	"acls": [
		// Allow all connections.
		{"action": "accept", "src": ["*"], "dst": ["*:*"]},
	],

	"ssh": [
		// Allow all users to SSH into their own devices in check mode.
		{
			"action": "check",
			"src":    ["autogroup:member"],
			"dst":    ["autogroup:self"],
			"users":  ["autogroup:nonroot", "root"],
		},
	],
}
`

const grantsPolicy = `{
	"tagOwners": {"tag:buildbox": ["autogroup:owner"]},
	"grants": [
		{"src": ["*"], "dst": ["*"], "ip": ["*"]},
	],
}
`

func testToken(t *testing.T, api *fakeAPI) AccessToken {
	t.Helper()

	server := httptest.NewServer(api.handler(t))
	t.Cleanup(server.Close)

	return AccessToken{Secret: testAccess, baseURL: server.URL}
}

func TestPlanSetupOnTheStockPolicy(t *testing.T) {
	t.Parallel()

	plan, err := PlanSetup(Policy{Raw: []byte(stockPolicy)}, testTag)
	if err != nil {
		t.Fatal(err)
	}

	if !plan.Pending() || plan.Grants || plan.StockRule != 0 || len(plan.Changes) != 3 {
		t.Fatalf("plan = %+v", plan)
	}

	for index, section := range []string{sectionTagOwners, sectionACLs, sectionSSH} {
		change := plan.Changes[index]
		if change.Section != section || change.Present || change.Line == "" {
			t.Fatalf("change %d = %+v", index, change)
		}
	}
}

func TestPlanSetupWithGrants(t *testing.T) {
	t.Parallel()

	plan, err := PlanSetup(Policy{Raw: []byte(grantsPolicy)}, testTag)
	if err != nil {
		t.Fatal(err)
	}

	if !plan.Grants || plan.StockRule != 0 || !plan.Changes[0].Present {
		t.Fatalf("plan = %+v", plan)
	}

	network := plan.Changes[1]
	if network.Section != sectionGrants || network.Present || !strings.Contains(network.Line, `"ip"`) {
		t.Fatalf("grant change = %+v", network)
	}
}

func TestApplySetupPatchesValidatesAndWrites(t *testing.T) {
	t.Parallel()

	api := &fakeAPI{policy: []byte(stockPolicy), etag: testETag}
	token := testToken(t, api)

	policy, err := token.ReadPolicy(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if policy.ETag != testETag || string(policy.Raw) != stockPolicy {
		t.Fatalf("read %q %q", policy.ETag, policy.Raw)
	}

	written := applyStock(t, token, policy)
	text := string(written.Raw)

	for _, want := range []string{"// Allow all connections.", `"tag:buildbox"`, ownerGroup, `"users"`, memberGroup} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}

	if written.ETag != testETag+"+1" || api.validated != 1 {
		t.Fatalf("etag %q validated %d", written.ETag, api.validated)
	}
}

func TestAppliedPolicyNeedsNothingMore(t *testing.T) {
	t.Parallel()

	token := testToken(t, &fakeAPI{policy: []byte(stockPolicy), etag: testETag})
	written := applyStock(t, token, Policy{Raw: []byte(stockPolicy), ETag: testETag})

	again, err := PlanSetup(written, testTag)
	if err != nil {
		t.Fatal(err)
	}

	if again.Pending() || again.StockRule != NoStockRule {
		t.Fatalf("after apply: %+v\n%s", again, written.Raw)
	}
}

// applyStock plans and applies the full setup, narrowing the stock rule.
func applyStock(t *testing.T, token AccessToken, policy Policy) Policy {
	t.Helper()

	plan, err := PlanSetup(policy, testTag)
	if err != nil {
		t.Fatal(err)
	}

	written, err := token.ApplySetup(context.Background(), policy, plan, true, testLogin)
	if err != nil {
		t.Fatal(err)
	}

	return written
}

func TestApplySetupRefusesAStalePolicy(t *testing.T) {
	t.Parallel()

	api := &fakeAPI{policy: []byte(stockPolicy), etag: testETag}
	token := testToken(t, api)

	plan, err := PlanSetup(Policy{Raw: []byte(stockPolicy)}, testTag)
	if err != nil {
		t.Fatal(err)
	}

	_, err = token.ApplySetup(
		context.Background(), Policy{Raw: []byte(stockPolicy), ETag: "stale"}, plan, false, testLogin)
	if err == nil || !strings.Contains(err.Error(), "changed while init") {
		t.Fatalf("err = %v", err)
	}

	if string(api.policy) != stockPolicy {
		t.Fatal("a stale write changed the policy")
	}
}

func TestApplySetupStopsOnValidationFailure(t *testing.T) {
	t.Parallel()

	api := &fakeAPI{policy: []byte(stockPolicy), etag: testETag, invalid: "test failed: alice cannot reach bob"}
	token := testToken(t, api)

	plan, err := PlanSetup(Policy{Raw: []byte(stockPolicy)}, testTag)
	if err != nil {
		t.Fatal(err)
	}

	_, err = token.ApplySetup(
		context.Background(), Policy{Raw: []byte(stockPolicy), ETag: testETag}, plan, false, testLogin)
	if err == nil || !strings.Contains(err.Error(), "rejected the new policy") || !strings.Contains(err.Error(), "alice") {
		t.Fatalf("err = %v", err)
	}

	if string(api.policy) != stockPolicy || api.etag != testETag {
		t.Fatal("a rejected policy was written")
	}
}

func TestApplySetupDoesNothingWhenComplete(t *testing.T) {
	t.Parallel()

	api := &fakeAPI{policy: []byte(stockPolicy), etag: testETag}
	token := testToken(t, api)

	plan := SetupPlan{StockRule: NoStockRule, Changes: []Change{{Section: sectionTagOwners, Present: true}}}

	same, err := token.ApplySetup(
		context.Background(), Policy{Raw: []byte(stockPolicy), ETag: testETag}, plan, false, testLogin)
	if err != nil {
		t.Fatal(err)
	}

	// Nothing to write, but the login is still proved against what is there.
	if same.ETag != testETag || api.validated != 1 || !strings.Contains(string(api.checked[0]), testLogin) {
		t.Fatalf("a complete plan: etag %q validated %d", same.ETag, api.validated)
	}
}

func TestApplySetupProvesTheLoginWithoutWritingTheTest(t *testing.T) {
	t.Parallel()

	api := &fakeAPI{policy: []byte(stockPolicy), etag: testETag}
	token := testToken(t, api)
	written := applyStock(t, token, Policy{Raw: []byte(stockPolicy), ETag: testETag})

	candidate := string(api.checked[len(api.checked)-1])
	for _, want := range []string{`"sshTests"`, testLogin, `"` + testTag + `"`, `"agent"`, `"admin"`, `"root"`} {
		if !strings.Contains(candidate, want) {
			t.Fatalf("validated without %s:\n%s", want, candidate)
		}
	}

	if strings.Contains(string(written.Raw), "sshTests") {
		t.Fatalf("the test was written:\n%s", written.Raw)
	}
}

func TestApplySetupStopsWhenTheLoginWouldBeRefused(t *testing.T) {
	t.Parallel()

	api := &fakeAPI{policy: []byte(stockPolicy), etag: testETag, failing: testLogin}
	token := testToken(t, api)

	plan, err := PlanSetup(Policy{Raw: []byte(stockPolicy), ETag: testETag}, testTag)
	if err != nil {
		t.Fatal(err)
	}

	_, err = token.ApplySetup(
		context.Background(), Policy{Raw: []byte(stockPolicy), ETag: testETag}, plan, true, testLogin)
	if err == nil || !strings.Contains(err.Error(), "would not let "+testLogin+" log in") ||
		!strings.Contains(err.Error(), "want: Accept, got: Drop") {
		t.Fatalf("err = %v", err)
	}

	if string(api.policy) != stockPolicy {
		t.Fatal("the policy was written despite the failed test")
	}
}

func TestValidationWarningsDoNotStopTheWrite(t *testing.T) {
	t.Parallel()

	api := &fakeAPI{policy: []byte(stockPolicy), etag: testETag, warning: true}
	token := testToken(t, api)

	written := applyStock(t, token, Policy{Raw: []byte(stockPolicy), ETag: testETag})
	if written.ETag == testETag {
		t.Fatal("a warning stopped the write")
	}
}

func TestCreateClientReturnsTheSecret(t *testing.T) {
	t.Parallel()

	api := &fakeAPI{}
	token := testToken(t, api)

	client, err := token.CreateClient(context.Background(), testTag)
	if err != nil {
		t.Fatal(err)
	}

	if client.ID != "kclient1" || client.Secret != "tskey-client-created" || !client.Configured() {
		t.Fatalf("client = %+v", client)
	}

	if len(api.clients) != 1 {
		t.Fatalf("clients = %v", api.clients)
	}

	request := api.clients[0]
	assertField(t, request, "keyType", "client")
	assertField(t, request, "scopes", "auth_keys")
	assertField(t, request, "tags", testTag)
}

// assertField checks a string, or a one-element list, in a decoded request.
func assertField(t *testing.T, request map[string]any, key, want string) {
	t.Helper()

	if list, ok := request[key].([]any); ok {
		if len(list) != 1 || list[0] != want {
			t.Fatalf("%s = %v", key, list)
		}

		return
	}

	if request[key] != want {
		t.Fatalf("%s = %v", key, request[key])
	}
}

// A rule in buildbox's own shape that lacks a box user is amended in place,
// keeping the users someone added; a rule in another shape is left alone and
// buildbox adds its own.
func TestSSHRuleIsAmendedNotDuplicated(t *testing.T) {
	t.Parallel()

	template := `{
	"tagOwners": {"TAG": ["autogroup:owner"]},
	"acls": [{"action": "accept", "src": ["autogroup:member"], "dst": ["*:*"]}],
	"ssh": [{"action": "accept", "src": SRC, "dst": ["TAG"], "users": USERS}],
}
`

	const owners = `["autogroup:owner"]`

	for _, check := range []struct {
		src, users string
		present    bool
		adds       []string
	}{
		{owners, `["agent", "admin"]`, true, nil},
		{owners, `["agent", "admin", "test"]`, true, nil},
		{owners, `["agent", "ubuntu"]`, false, []string{config.AdminUser}},
		{owners, `["test"]`, false, boxUsers()},
		{`["group:dev"]`, `["agent"]`, false, nil},
	} {
		raw := strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(template, "TAG", testTag), "SRC", check.src),
			"USERS", check.users)

		plan, err := PlanSetup(Policy{Raw: []byte(raw)}, testTag)
		if err != nil {
			t.Fatal(err)
		}

		ssh := plan.Changes[len(plan.Changes)-1]
		if ssh.Present != check.present || !slices.Equal(ssh.Adds, check.adds) {
			t.Fatalf("%s %s: present %v adds %v", check.src, check.users, ssh.Present, ssh.Adds)
		}
	}
}

func TestAmendingKeepsOneRuleAndEveryUser(t *testing.T) {
	t.Parallel()

	policy := strings.ReplaceAll(`{
	"tagOwners": {"TAG": ["autogroup:owner"]},
	"acls": [{"action": "accept", "src": ["autogroup:owner"], "dst": ["TAG:*"]}],
	"ssh": [
		// buildbox's rule, from before admin was the admin user, extended by hand.
		{"action": "accept", "src": ["autogroup:owner"], "dst": ["TAG"], "users": ["agent", "ubuntu", "test"]},
	],
}
`, "TAG", testTag)

	api := &fakeAPI{policy: []byte(policy), etag: testETag}
	token := testToken(t, api)

	plan, err := PlanSetup(Policy{Raw: []byte(policy), ETag: testETag}, testTag)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(plan.Changes[2].Line, `"users": ["agent", "ubuntu", "test", "admin"]`) {
		t.Fatalf("line = %s", plan.Changes[2].Line)
	}

	written, err := token.ApplySetup(context.Background(), Policy{Raw: []byte(policy), ETag: testETag}, plan, false, "")
	if err != nil {
		t.Fatal(err)
	}

	text := string(written.Raw)
	if strings.Count(text, `"users"`) != 1 || !strings.Contains(text, "before admin was the admin user") {
		t.Fatalf("written:\n%s", text)
	}

	for _, user := range []string{config.AgentUser, "ubuntu", "test", config.AdminUser} {
		if !strings.Contains(text, `"`+user+`"`) {
			t.Fatalf("missing %s:\n%s", user, text)
		}
	}
}
