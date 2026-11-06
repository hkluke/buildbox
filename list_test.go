package main

import (
	"bytes"
	"testing"
	"time"

	"github.com/hkluke/buildbox/internal/config"
	"github.com/hkluke/buildbox/internal/console"
	"github.com/hkluke/buildbox/internal/tailscale"
)

func TestFormatAgeRoundsDown(t *testing.T) {
	t.Parallel()

	for _, check := range []struct {
		age  time.Duration
		want string
	}{
		{30 * time.Second, "<1m"},
		{59 * time.Minute, "59m"},
		{47*time.Hour + 59*time.Minute, "47h"},
		{50 * time.Hour, "2d"},
	} {
		if got := formatAge(check.age); got != check.want {
			t.Fatalf("formatAge(%s) = %q, want %q", check.age, got, check.want)
		}
	}
}

func TestListTableAlignsColumns(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	printBuildBoxTable(console.NewWriter(&buf), []buildBox{
		{testBox, stateRunning, config.DefaultType, config.SeedLocation, now.Add(-3 * time.Hour), testAddress},
		{"old-box", stateOff, "cpx22", "hel1", now.Add(-12 * hoursPerDay * time.Hour), tailscale.PeerAbsent},
	}, now)

	want := "NAME     STATUS   TYPE   LOCATION  AGE  TAILSCALE\n" +
		"dev      running  cx23   fsn1      3h   100.64.0.2\n" +
		"old-box  off      cpx22  hel1      12d  absent\n"
	if buf.String() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestListJSONRecordsKeepTheFacts(t *testing.T) {
	t.Parallel()

	created := time.Date(2026, 10, 23, 9, 30, 0, 0, time.FixedZone("CEST", 2*60*60))

	present := buildBox{testBox, stateRunning, config.DefaultType, config.SeedLocation, created, testAddress}.record()
	if present.Tailnet != tailnetPresent || present.Address == nil || *present.Address != testAddress {
		t.Fatalf("present = %+v", present)
	}

	if present.Created != "2026-10-23T07:30:00Z" {
		t.Fatalf("created = %q", present.Created)
	}

	for _, peer := range []string{tailscale.PeerAbsent, tailscale.PeerSelf} {
		record := buildBox{testBox, stateRunning, config.DefaultType, config.SeedLocation, created, peer}.record()
		if record.Tailnet != peer || record.Address != nil {
			t.Fatalf("%s = %+v", peer, record)
		}
	}
}
