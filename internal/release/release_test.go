package release

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

const (
	testCurrent = "v0.1.0"
	testNewer   = "v0.2.0"
	latestPath  = "/github.com/hkluke/buildbox/@latest"
)

func TestReleasedIgnoresDevelopmentBuilds(t *testing.T) {
	t.Parallel()

	for version, want := range map[string]bool{
		testCurrent:                         true,
		"v1.2.3":                            true,
		"dev":                               false,
		"(devel)":                           false,
		"v0.1.1-0.20261030120000-abcdef123": false,
		"v0.1.0+dirty":                      false,
		"v0.2.0-rc.1":                       false,
	} {
		if got := Released(version); got != want {
			t.Errorf("Released(%q) = %v", version, got)
		}
	}
}

// proxy answers @latest with body, and counts the requests it gets.
func proxy(t *testing.T, status int, body string) (Checker, *atomic.Int32) {
	t.Helper()

	var calls atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)

		if request.URL.Path != latestPath || request.Header.Get("User-Agent") != "buildbox/test" {
			http.NotFound(writer, request)

			return
		}

		writer.WriteHeader(status)
		_, _ = writer.Write([]byte(body))
	}))
	t.Cleanup(server.Close)

	return Checker{Proxy: server.URL, Client: server.Client(), Agent: "buildbox/test"}, &calls
}

func TestNewerFindsANewerRelease(t *testing.T) {
	t.Parallel()

	checker, _ := proxy(t, http.StatusOK, `{"Version":"`+testNewer+`","Time":"2026-11-01T10:00:00Z"}`)

	got, err := checker.Newer(t.Context(), testCurrent)
	if err != nil || got != testNewer {
		t.Fatalf("Newer = %q, %v", got, err)
	}
}

func TestNewerIsEmptyWhenUpToDate(t *testing.T) {
	t.Parallel()

	for _, latest := range []string{testCurrent, "v0.0.9", "v0.3.0-rc.1", "v0.1.1-0.20261030120000-abcdef123"} {
		checker, _ := proxy(t, http.StatusOK, `{"Version":"`+latest+`"}`)

		got, err := checker.Newer(t.Context(), testCurrent)
		if err != nil || got != "" {
			t.Fatalf("latest %s: Newer = %q, %v", latest, got, err)
		}
	}
}

func TestNewerSkipsDevelopmentBuildsWithoutAsking(t *testing.T) {
	t.Parallel()

	checker, calls := proxy(t, http.StatusOK, `{"Version":"`+testNewer+`"}`)

	got, err := checker.Newer(t.Context(), "dev")
	if err != nil || got != "" || calls.Load() != 0 {
		t.Fatalf("Newer = %q, %v after %d requests", got, err, calls.Load())
	}
}

func TestNewerReportsAProxyThatHasNoAnswer(t *testing.T) {
	t.Parallel()

	for status, body := range map[int]string{http.StatusNotFound: "not found", http.StatusOK: "not json"} {
		checker, _ := proxy(t, status, body)

		got, err := checker.Newer(t.Context(), testCurrent)
		if err == nil || got != "" {
			t.Fatalf("status %d: Newer = %q, %v", status, got, err)
		}
	}
}
