package tailscale

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hkluke/buildbox/internal/abort"
)

const (
	apiBase = "https://api.tailscale.com"
	//nolint:gosec // G101: a URL path, not a credential.
	tokenPath      = "/api/v2/oauth/token"
	keysPath       = "/api/v2/tailnet/-/keys"
	apiCallTimeout = 20 * time.Second
	authKeyTTL     = time.Hour
	checkKeyTTL    = time.Minute
	maxAPIBody     = 1 << 20
	maxAPIMessage  = 200
	statusFamily   = 100
	statusOK       = 2
	jsonType       = "application/json"
	formType       = "application/x-www-form-urlencoded"
	oauthClient    = "OAuth client"
)

// OAuthClient mints auth keys through the Tailscale API. It is created once,
// by init or in the admin console, with the auth_keys scope and the boxes'
// tag, and can do nothing else: it cannot read devices or change the policy.
// Only the one-time key it mints reaches a box.
type OAuthClient struct {
	ID     string
	Secret string

	// baseURL replaces the API host in tests.
	baseURL string
}

// Configured reports whether both halves are set.
func (client OAuthClient) Configured() bool {
	return client.ID != "" && client.Secret != ""
}

// MintAuthKey creates a one-time, ephemeral, pre-approved auth key for tag.
// The key is valid for an hour and spent by the first device that uses it.
func (client OAuthClient) MintAuthKey(ctx context.Context, tag string) (string, error) {
	minted, err := client.mint(ctx, tag, authKeyTTL)
	if err != nil {
		return "", err
	}

	return minted.secret, nil
}

// Check proves the client works for tag by minting a key that lives a minute
// and deleting it again. It fails the way MintAuthKey would in create.
func (client OAuthClient) Check(ctx context.Context, tag string) error {
	minted, err := client.mint(ctx, tag, checkKeyTTL)
	if err != nil {
		return err
	}

	_, err = client.api().do(ctx, apiCall{
		method:     http.MethodDelete,
		path:       keysPath + "/" + url.PathEscape(minted.id),
		bearer:     minted.bearer,
		tag:        tag,
		credential: oauthClient,
	}, nil)

	return err
}

func (client OAuthClient) api() api {
	return api{base: client.baseURL}
}

type mintedKey struct {
	id     string
	secret string
	bearer string
}

type keyRequest struct {
	Capabilities  keyCapabilities `json:"capabilities"`
	ExpirySeconds int64           `json:"expirySeconds"`
}

type keyCapabilities struct {
	Devices keyDevices `json:"devices"`
}

type keyDevices struct {
	Create keyCreate `json:"create"`
}

type keyCreate struct {
	Reusable      bool     `json:"reusable"`
	Ephemeral     bool     `json:"ephemeral"`
	Preauthorized bool     `json:"preauthorized"`
	Tags          []string `json:"tags"`
}

func (client OAuthClient) mint(ctx context.Context, tag string, ttl time.Duration) (mintedKey, error) {
	bearer, err := client.token(ctx, tag)
	if err != nil {
		return mintedKey{}, err
	}

	body, err := json.Marshal(keyRequest{
		Capabilities: keyCapabilities{Devices: keyDevices{Create: keyCreate{
			Reusable:      false,
			Ephemeral:     true,
			Preauthorized: true,
			Tags:          []string{tag},
		}}},
		ExpirySeconds: int64(ttl / time.Second),
	})
	if err != nil {
		return mintedKey{}, fmt.Errorf("encode the key request: %w", err)
	}

	var created struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}

	_, err = client.api().do(ctx, apiCall{
		method:      http.MethodPost,
		path:        keysPath,
		bearer:      bearer,
		contentType: jsonType,
		body:        bytes.NewReader(body),
		tag:         tag,
		credential:  oauthClient,
	}, &created)
	if err != nil {
		return mintedKey{}, err
	}

	if created.Key == "" {
		return mintedKey{}, abort.New("The Tailscale API returned an auth key without its secret.")
	}

	return mintedKey{id: created.ID, secret: created.Key, bearer: bearer}, nil
}

// token exchanges the client secret for an access token, which Tailscale
// keeps valid for an hour. One is fetched per mint.
func (client OAuthClient) token(ctx context.Context, tag string) (string, error) {
	form := url.Values{"client_id": {client.ID}, "client_secret": {client.Secret}}

	var granted struct {
		AccessToken string `json:"access_token"` //nolint:tagliatelle // the OAuth token endpoint uses snake_case.
	}

	_, err := client.api().do(ctx, apiCall{
		method:      http.MethodPost,
		path:        tokenPath,
		contentType: formType,
		body:        strings.NewReader(form.Encode()),
		tag:         tag,
		credential:  oauthClient,
	}, &granted)
	if err != nil {
		return "", err
	}

	if granted.AccessToken == "" {
		return "", abort.New("The Tailscale API returned no access token.")
	}

	return granted.AccessToken, nil
}

// api is the Tailscale API host. The zero value is the real one.
type api struct {
	base string
}

type apiCall struct {
	method      string
	path        string
	bearer      string
	contentType string
	accept      string
	ifMatch     string
	body        io.Reader
	// tag and credential word the error when the API refuses the call.
	tag        string
	credential string
}

// do sends one request. A 2xx answer is decoded into out: a *[]byte takes
// the body as it is, anything else is JSON. The headers come back for the
// policy's ETag.
func (client api) do(ctx context.Context, req apiCall, out any) (http.Header, error) {
	ctx, cancel := context.WithTimeout(ctx, apiCallTimeout)
	defer cancel()

	response, err := client.send(ctx, req)
	if err != nil {
		return nil, err
	}

	defer func() { _ = response.Body.Close() }()

	payload, err := io.ReadAll(io.LimitReader(response.Body, maxAPIBody))
	if err != nil {
		return nil, abort.Newf("Could not read the Tailscale API answer.\n%s", err.Error())
	}

	if response.StatusCode/statusFamily != statusOK {
		return nil, apiFailure(response.StatusCode, payload, req)
	}

	switch target := out.(type) {
	case nil:
	case *[]byte:
		*target = payload
	default:
		err = json.Unmarshal(payload, out)
		if err != nil {
			return nil, abort.Newf("Could not read the Tailscale API answer.\n%s", err.Error())
		}
	}

	return response.Header, nil
}

func (client api) send(ctx context.Context, req apiCall) (*http.Response, error) {
	body := req.body
	if body == nil {
		body = http.NoBody
	}

	request, err := http.NewRequestWithContext(ctx, req.method, client.baseURL()+req.path, body)
	if err != nil {
		return nil, fmt.Errorf("build the API request: %w", err)
	}

	for name, value := range map[string]string{
		"Content-Type": req.contentType,
		"Accept":       req.accept,
		"If-Match":     req.ifMatch,
	} {
		if value != "" {
			request.Header.Set(name, value)
		}
	}

	if req.bearer != "" {
		request.Header.Set("Authorization", "Bearer "+req.bearer)
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, abort.Newf("Could not reach the Tailscale API.\n%s", err.Error())
	}

	return response, nil
}

func (client api) baseURL() string {
	if client.base != "" {
		return client.base
	}

	return apiBase
}

// apiFailure turns an API error into advice. A refused credential is the
// usual problem: an OAuth client lacks the auth_keys scope or the tag, or an
// access token is not an admin's.
func apiFailure(status int, payload []byte, req apiCall) error {
	message := apiMessage(payload)

	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return abort.Hint("The Tailscale API refused the "+req.credential, message, refusedHint(req))
	case http.StatusPreconditionFailed:
		return abort.Hint(
			"The tailnet policy changed while init was running",
			message,
			"Nothing was written. Run init again; it reads the policy afresh.",
		)
	default:
		return abort.Hint(fmt.Sprintf("The Tailscale API answered %d", status), message, answeredHint(req))
	}
}

func refusedHint(req apiCall) string {
	if req.credential == oauthClient {
		return "Check the client in the admin console: it needs the auth_keys scope and the tag " + req.tag + ".\n" +
			abort.Indent + OAuthURL
	}

	return "Generate a new access token as an admin of the tailnet:\n" + abort.Indent + KeysURL
}

func answeredHint(req apiCall) string {
	if req.tag != "" {
		return "The tag " + req.tag + " must be in the policy's tagOwners before a key can carry it.\n" +
			abort.Indent + PolicyURL
	}

	return "Nothing was written. The policy can be fixed in the admin console:\n" + abort.Indent + PolicyURL
}

// apiMessage is the API's own explanation, when it sent one as JSON.
func apiMessage(payload []byte) string {
	if message := jsonMessage(payload); message != "" {
		return message
	}

	line, _, _ := strings.Cut(strings.TrimSpace(string(payload)), "\n")
	if len(line) > maxAPIMessage {
		line = line[:maxAPIMessage]
	}

	return line
}

// jsonMessage is the message field of a JSON error body, or empty.
func jsonMessage(payload []byte) string {
	var body struct {
		Message string `json:"message"`
	}

	err := json.Unmarshal(payload, &body)
	if err != nil {
		return ""
	}

	return body.Message
}
