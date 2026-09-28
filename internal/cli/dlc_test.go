package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/core"
)

// TestParseDLCSelectors locks the flag contract: both flags repeat, and they
// collect into ONE ordered list, so the selection report can follow the order
// the user typed rather than the order the flags happen to be stored in.
func TestParseDLCSelectors(t *testing.T) {
	inv := parseOpts(t, "install", "123", "--dlc", "A", "--exclude-dlc", "B", "--dlc", "C")

	want := []config.DLCSelector{
		{Value: "A"},
		{Value: "B", Exclude: true},
		{Value: "C"},
	}
	got := inv.cfg.DownloadConfig.DLCSelectors
	if len(got) != len(want) {
		t.Fatalf("selectors = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("selector[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestInstallDoesNotAcceptTheIncludeExcludeMasks locks the invariant the DLC
// selection is defined against: install carries no product-level mask flags, so
// a conflict between --dlc and one is not a state this CLI can reach, and none
// is specified.
//
// The test exists so the surface cannot drift silently: if the masks are ever
// added to install, this fails and their interaction with --dlc / --exclude-dlc
// has to be decided by that change rather than inferred from this feature.
func TestInstallDoesNotAcceptTheIncludeExcludeMasks(t *testing.T) {
	for _, args := range [][]string{
		{"install", "123", "--exclude", "dlc", "--dlc", "Foo"},
		{"install", "123", "--include", "installers", "--dlc", "Foo"},
	} {
		_, err := parseArgs(args, testDefaults())
		if err == nil {
			t.Fatalf("%v: install must not accept the include/exclude masks", args)
		}
		if !strings.Contains(err.Error(), "is not accepted by") {
			t.Errorf("%v: err = %v, want an unaccepted-option refusal", args, err)
		}
	}
}

// TestRenderInstallOptionsDLCs locks the listing's DLC section: the product id
// --dlc accepts, the title, and the ownership answer, because either identity
// may be the value the user has to hand.
func TestRenderInstallOptionsDLCs(t *testing.T) {
	res := core.InstallOptionsResult{
		GameTitle: "Worms United",
		BuildID:   "b1",
		Entries: []core.InstallOptionEntry{
			{Platform: "windows", Arch: "x64", Language: "en-US", Size: 1024, Files: 2},
		},
		DLCs: []core.DLCOption{
			{ProductID: "111", Title: "Legacy of Rome", Owned: true},
			{ProductID: "222", Title: "Sunset Invasion", Owned: false},
		},
	}

	var buf bytes.Buffer
	if err := renderInstallOptions(&buf, res); err != nil {
		t.Fatalf("renderInstallOptions: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"PLATFORM", "ARCH", "LANGUAGE",
		"DLCs (install some of them with --dlc <id|title>):",
		"PRODUCT ID", "TITLE", "OWNED",
		"111", "Legacy of Rome", "yes",
		"222", "Sunset Invasion", "no",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// TestRenderInstallOptionsWithoutDLCs locks that a build with no DLCs prints no
// empty section.
func TestRenderInstallOptionsWithoutDLCs(t *testing.T) {
	res := core.InstallOptionsResult{
		GameTitle: "Worms United",
		BuildID:   "b1",
		Entries: []core.InstallOptionEntry{
			{Platform: "windows", Arch: "x64", Language: "en-US", Size: 1024, Files: 2},
		},
	}

	var buf bytes.Buffer
	if err := renderInstallOptions(&buf, res); err != nil {
		t.Fatalf("renderInstallOptions: %v", err)
	}
	if strings.Contains(buf.String(), "DLCs") {
		t.Errorf("output must not carry an empty DLC section:\n%s", buf.String())
	}
}
