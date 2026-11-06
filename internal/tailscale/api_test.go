package tailscale

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

const (
	testTag = "tag:buildbox"
	//nolint:gosec // G101: a made-up value for the fake API.
	goodSecret = "tskey-client-good"
)

// fakeAPI is enough of api.tailscale.com for the OAuth client: a token
// endpoint and the keys endpoint, recording what the client asked for.
type fakeAPI struct {
	mu        sync.Mutex
	secret    string
	created   []map[string]any
	deleted   []string
	rejected  int
	policy    []byte
	etag      string
	invalid   string
	failing   string
	warning   bool
	validated int
	checked   [][]byte
	clients   []map[string]any
}

func (api *fakeAPI) handler(t *testing.T) http.Handler {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc(tokenPath, api.token(t))
	mux.HandleFunc(keysPath, api.createKey(t))
	mux.HandleFunc(keysPath+"/", api.deleteKey(t))
	mux.HandleFunc(policyPath, api.policyFile(t))
	mux.HandleFunc(validatePath, api.validate(t))

	return mux
}

// policyFile serves the policy with an ETag and accepts a write that quotes
// the current one, the way the real API does.
func (api *fakeAPI) policyFile(t *testing.T) http.HandlerFunc {
	t.Helper()

	return func(writer http.ResponseWriter, request *http.Request) {
		api.mu.Lock()
		defer api.mu.Unlock()

		if request.Header.Get("Authorization") == "" {
			writer.WriteHeader(http.StatusUnauthorized)

			return
		}

		switch request.Method {
		case http.MethodGet:
			writer.Header().Set("ETag", api.etag)
			_, _ = writer.Write(api.policy)
		case http.MethodPost:
			if request.Header.Get("If-Match") != api.etag {
				writer.WriteHeader(http.StatusPreconditionFailed)
				_, _ = writer.Write([]byte(`{"message":"precondition failed, invalid old hash"}`))

				return
			}

			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Errorf("read policy: %v", err)
			}

			api.policy = body
			api.etag += "+1"
			writer.Header().Set("ETag", api.etag)
			_, _ = writer.Write(body)
		default:
			writer.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func (api *fakeAPI) validate(t *testing.T) http.HandlerFunc {
	t.Helper()

	return func(writer http.ResponseWriter, request *http.Request) {
		api.mu.Lock()
		defer api.mu.Unlock()

		api.validated++

		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read candidate: %v", err)
		}

		api.checked = append(api.checked, body)

		switch {
		case api.invalid != "":
			_, _ = writer.Write([]byte(`{"message":` + strconv.Quote(api.invalid) + `}`))
		case api.failing != "":
			_, _ = writer.Write([]byte(`{"message":"test(s) failed","data":[{"user":` + strconv.Quote(api.failing) +
				`,"errors":["ssh to ` + testTag + ` as admin: want: Accept, got: Drop"]}]}`))
		case api.warning:
			_, _ = writer.Write([]byte(`{"message":"warning(s) found","data":[{"user":"group:x",` +
				`"warnings":["group is not syncing from SCIM"]}]}`))
		default:
			_, _ = writer.Write([]byte("{}"))
		}
	}
}

func (api *fakeAPI) token(t *testing.T) http.HandlerFunc {
	t.Helper()

	return func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.FormValue("client_secret") != api.secret {
			writer.WriteHeader(http.StatusUnauthorized)
			_, _ = writer.Write([]byte(`{"message":"invalid client"}`))

			return
		}

		_, _ = writer.Write([]byte(`{"access_token":"token-1","token_type":"Bearer","expires_in":3600}`))
	}
}

func (api *fakeAPI) createKey(t *testing.T) http.HandlerFunc {
	t.Helper()

	return func(writer http.ResponseWriter, request *http.Request) {
		bearer := request.Header.Get("Authorization")
		if bearer != "Bearer token-1" && bearer != "Bearer "+testAccess {
			writer.WriteHeader(http.StatusUnauthorized)

			return
		}

		var body map[string]any

		err := json.NewDecoder(request.Body).Decode(&body)
		if err != nil {
			t.Errorf("decode key request: %v", err)
		}

		api.mu.Lock()
		defer api.mu.Unlock()

		if api.rejected != 0 {
			writer.WriteHeader(api.rejected)
			_, _ = writer.Write([]byte(`{"message":"requested tags [tag:nope] are invalid or not permitted"}`))

			return
		}

		if body["keyType"] == "client" {
			api.clients = append(api.clients, body)
			_, _ = writer.Write([]byte(`{"id":"kclient1","key":"tskey-client-created","keyType":"client"}`))

			return
		}

		api.created = append(api.created, body)
		_, _ = writer.Write([]byte(`{"id":"k1","key":"tskey-auth-minted","created":"2026-10-26T10:00:00Z"}`))
	}
}

func (api *fakeAPI) deleteKey(t *testing.T) http.HandlerFunc {
	t.Helper()

	return func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodDelete {
			writer.WriteHeader(http.StatusMethodNotAllowed)

			return
		}

		api.mu.Lock()
		defer api.mu.Unlock()

		api.deleted = append(api.deleted, strings.TrimPrefix(request.URL.Path, keysPath+"/"))
	}
}

func testClient(t *testing.T, api *fakeAPI, secret string) OAuthClient {
	t.Helper()

	server := httptest.NewServer(api.handler(t))
	t.Cleanup(server.Close)

	return OAuthClient{ID: "k123", Secret: secret, baseURL: server.URL}
}

func TestMintAuthKeyAsksForAOneTimeTaggedKey(t *testing.T) {
	t.Parallel()

	api := &fakeAPI{secret: goodSecret}
	client := testClient(t, api, goodSecret)

	key, err := client.MintAuthKey(context.Background(), testTag)
	if err != nil {
		t.Fatal(err)
	}

	if key != "tskey-auth-minted" {
		t.Fatalf("key = %q", key)
	}

	if len(api.created) != 1 {
		t.Fatalf("created %d keys", len(api.created))
	}

	payload, err := json.Marshal(api.created[0])
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		`"reusable":false`, `"ephemeral":true`, `"preauthorized":true`, `"tags":["` + testTag + `"]`, `"expirySeconds":3600`,
	} {
		if !strings.Contains(string(payload), want) {
			t.Fatalf("missing %s in %s", want, payload)
		}
	}
}

func TestCheckMintsAndDeletesAShortLivedKey(t *testing.T) {
	t.Parallel()

	api := &fakeAPI{secret: goodSecret}
	client := testClient(t, api, goodSecret)

	err := client.Check(context.Background(), testTag)
	if err != nil {
		t.Fatal(err)
	}

	if len(api.created) != 1 || len(api.deleted) != 1 || api.deleted[0] != "k1" {
		t.Fatalf("created %v deleted %v", api.created, api.deleted)
	}

	payload, err := json.Marshal(api.created[0])
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(payload), `"expirySeconds":60`) {
		t.Fatalf("check key should live a minute: %s", payload)
	}
}

func TestRefusedClientPointsAtTheConsole(t *testing.T) {
	t.Parallel()

	api := &fakeAPI{secret: goodSecret}
	client := testClient(t, api, "tskey-client-wrong")

	_, err := client.MintAuthKey(context.Background(), testTag)
	if err == nil {
		t.Fatal("a wrong secret was accepted")
	}

	for _, want := range []string{"refused the OAuth client", "invalid client", "auth_keys", testTag, OAuthURL} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("missing %q in %v", want, err)
		}
	}
}

func TestRefusedTagPointsAtThePolicy(t *testing.T) {
	t.Parallel()

	api := &fakeAPI{secret: goodSecret, rejected: http.StatusBadRequest}
	client := testClient(t, api, goodSecret)

	err := client.Check(context.Background(), "tag:nope")
	if err == nil {
		t.Fatal("a refused tag was accepted")
	}

	for _, want := range []string{"answered 400", "not permitted", "tagOwners", PolicyURL} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("missing %q in %v", want, err)
		}
	}
}

func TestOAuthClientNeedsBothHalves(t *testing.T) {
	t.Parallel()

	if (OAuthClient{ID: "k"}).Configured() || (OAuthClient{Secret: "s"}).Configured() {
		t.Fatal("half a client is configured")
	}

	if !(OAuthClient{ID: "k", Secret: "s"}).Configured() {
		t.Fatal("a whole client is not")
	}
}

func TestPolicyBlocksNameTheTagAndTheUsers(t *testing.T) {
	t.Parallel()

	blocks := PolicyBlocks("tag:dev")
	if len(blocks) != 3 || blocks[0].Section != "tagOwners" || blocks[1].Section != "acls" || blocks[2].Section != "ssh" {
		t.Fatalf("blocks = %+v", blocks)
	}

	joined := blocks[0].Line + blocks[1].Line + blocks[2].Line

	wants := []string{`"tag:dev": ["autogroup:owner"]`, `"dst": ["tag:dev:*"]`, `"users": ["agent", "admin"]`}
	for _, want := range wants {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %s in %s", want, joined)
		}
	}

	if strings.Contains(joined, "root") || strings.Contains(joined, "autogroup:member") {
		t.Fatalf("rule too wide: %s", joined)
	}
}
