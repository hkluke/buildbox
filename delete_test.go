package main

import (
	"strings"
	"testing"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"

	"github.com/hkluke/buildbox/internal/hetzner"
)

func TestDeleteParsesForce(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{testBox, flagForce}, {flagForce, "--name=dev"}} {
		name, force, err := parseDeleteArgs(args)
		if err != nil || name != testBox || !force {
			t.Fatalf("%v: name = %q, force = %v, err = %v", args, name, force, err)
		}
	}

	_, force, err := parseDeleteArgs([]string{testBox})
	if err != nil || force {
		t.Fatalf("force = %v, err = %v", force, err)
	}
}

func TestDeleteKeepsToLabeledServers(t *testing.T) {
	t.Parallel()

	labeled := &hcloud.Server{Name: testBox, Labels: map[string]string{hetzner.LabelKey: hetzner.LabelValue}}
	other := &hcloud.Server{Name: "db", Labels: map[string]string{"role": "database"}}

	if requireLabel(nil, false) != nil || requireLabel(labeled, false) != nil || requireLabel(other, true) != nil {
		t.Fatal("refused a server it should delete")
	}

	err := requireLabel(other, false)
	if err == nil || !strings.Contains(err.Error(), flagForce) {
		t.Fatalf("err = %v", err)
	}
}
