package core

import (
	"context"
	"testing"

	"github.com/nekrozis/goggo/internal/galaxy"
)

func TestInstallOptionsAggregation(t *testing.T) {
	f := newPlanFixture(t)
	f.setDefaultBodies()

	v2New := galaxy.HashToGalaxyPath(planBuildHashNew)
	depotHashEN64 := "a111111111111111111111111111111111111111"
	depotHashEN32 := "a222222222222222222222222222222222222222"
	depotHashFR64 := "a333333333333333333333333333333333333333"
	depotHashSupport := "a444444444444444444444444444444444444444"
	depotHashDLC := "a555555555555555555555555555555555555555"

	f.set("/content-system/v2/meta/"+v2New,
		`{"baseProductId":"`+planProductID+`","installDirectory":"TestGame","version":2,`+
			`"products":[{"name":"Test Game"}],`+
			`"depots":[`+
			`{"productId":"`+planProductID+`","languages":["en-US"],"osBitness":["64"],"size":1000,"manifest":"`+depotHashEN64+`"},`+
			`{"productId":"`+planProductID+`","languages":["en-US"],"osBitness":["32"],"size":900,"manifest":"`+depotHashEN32+`"},`+
			`{"productId":"`+planProductID+`","languages":["fr-FR"],"osBitness":["64"],"size":1100,"manifest":"`+depotHashFR64+`"},`+
			`{"productId":"`+planProductID+`","isGogDepot":true,"languages":["*"],"size":50,"manifest":"`+depotHashSupport+`"},`+
			`{"productId":"dlc1","languages":["en-US"],"osBitness":["64"],"size":500,"manifest":"`+depotHashDLC+`"}]}`)

	f.set("/content-system/v2/meta/"+galaxy.HashToGalaxyPath(depotHashEN64),
		`{"depot":{"items":[`+
			`{"path":"game/bin.exe","chunks":[{"compressedMd5":"c1","md5":"u1","compressedSize":500,"size":500}]},`+
			`{"path":"game/data1.bin","chunks":[{"compressedMd5":"c2","md5":"u2","compressedSize":500,"size":500}]}]}}`)

	f.set("/content-system/v2/meta/"+galaxy.HashToGalaxyPath(depotHashEN32),
		`{"depot":{"items":[`+
			`{"path":"game/bin.exe","chunks":[{"compressedMd5":"c3","md5":"u3","compressedSize":450,"size":450}]},`+
			`{"path":"game/data1.bin","chunks":[{"compressedMd5":"c4","md5":"u4","compressedSize":450,"size":450}]}]}}`)

	f.set("/content-system/v2/meta/"+galaxy.HashToGalaxyPath(depotHashFR64),
		`{"depot":{"items":[`+
			`{"path":"game/bin.exe","chunks":[{"compressedMd5":"c5","md5":"u5","compressedSize":550,"size":550}]},`+
			`{"path":"game/data_fr.bin","chunks":[{"compressedMd5":"c6","md5":"u6","compressedSize":550,"size":550}]}]}}`)

	f.set("/content-system/v2/meta/"+galaxy.HashToGalaxyPath(depotHashSupport),
		`{"depot":{"items":[{"path":"goggame.info","chunks":[{"compressedMd5":"c7","md5":"u7","compressedSize":50,"size":50}]}]}}`)

	f.set("/content-system/v2/meta/"+galaxy.HashToGalaxyPath(depotHashDLC),
		`{"depot":{"items":[{"path":"dlc/extra.bin","chunks":[{"compressedMd5":"c8","md5":"u8","compressedSize":500,"size":500}]}]}}`)

	cfg := planTestConfig(t)
	d := newOfflineDownloader(t, f.Server, cfg, newFakeConsole())

	res, err := d.InstallOptions(context.Background(), planProductID, ProductRefExact, "windows", "")
	if err != nil {
		t.Fatalf("InstallOptions: %v", err)
	}

	if res.GameTitle != "Test Game" {
		t.Errorf("GameTitle = %q, want 'Test Game'", res.GameTitle)
	}
	if res.BuildID != "b-new" {
		t.Errorf("BuildID = %q, want 'b-new'", res.BuildID)
	}

	// Must contain exactly 3 combinations:
	// 1. windows x64 en-US (1000 bytes, 2 files)
	// 2. windows x86 en-US (900 bytes, 2 files)
	// 3. windows x64 fr-FR (1100 bytes, 2 files)
	if len(res.Entries) != 3 {
		t.Fatalf("len(Entries) = %d, want 3: %+v", len(res.Entries), res.Entries)
	}

	// Verify entries order and values.
	// Entry 0: windows x64 en-US
	e0 := res.Entries[0]
	if e0.Platform != "windows" || e0.Arch != "x64" || e0.Language != "en-US" || e0.Size != 1000 || e0.Files != 2 {
		t.Errorf("entry[0] mismatch: %+v", e0)
	}

	// Entry 1: windows x64 fr-FR
	e1 := res.Entries[1]
	if e1.Platform != "windows" || e1.Arch != "x64" || e1.Language != "fr-FR" || e1.Size != 1100 || e1.Files != 2 {
		t.Errorf("entry[1] mismatch: %+v", e1)
	}

	// Entry 2: windows x86 en-US
	e2 := res.Entries[2]
	if e2.Platform != "windows" || e2.Arch != "x86" || e2.Language != "en-US" || e2.Size != 900 || e2.Files != 2 {
		t.Errorf("entry[2] mismatch: %+v", e2)
	}
}

func TestInstallOptionsMultipleDepotsPerTuple(t *testing.T) {
	// Case F: Same tuple multiple depots aggregation.
	// Base game has a shared base depot + an English voice depot for the same (x64, en-US) tuple.
	f := newPlanFixture(t)
	f.setDefaultBodies()

	v2New := galaxy.HashToGalaxyPath(planBuildHashNew)
	depotShared := "aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111"
	depotVoice := "bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222"

	f.set("/content-system/v2/meta/"+v2New,
		`{"baseProductId":"`+planProductID+`","installDirectory":"VoiceGame","version":2,`+
			`"products":[{"name":"Voice Game"}],`+
			`"depots":[`+
			`{"productId":"`+planProductID+`","languages":["*"],"osBitness":["64"],"size":2000,"manifest":"`+depotShared+`"},`+
			`{"productId":"`+planProductID+`","languages":["en-US"],"osBitness":["64"],"size":500,"manifest":"`+depotVoice+`"}]}`)

	f.set("/content-system/v2/meta/"+galaxy.HashToGalaxyPath(depotShared),
		`{"depot":{"items":[`+
			`{"path":"game/engine.dll","chunks":[{"compressedMd5":"c1","md5":"u1","compressedSize":1000,"size":1000}]},`+
			`{"path":"game/data.pak","chunks":[{"compressedMd5":"c2","md5":"u2","compressedSize":1000,"size":1000}]}]}}`)

	f.set("/content-system/v2/meta/"+galaxy.HashToGalaxyPath(depotVoice),
		`{"depot":{"items":[`+
			`{"path":"game/voice_en.pak","chunks":[{"compressedMd5":"c3","md5":"u3","compressedSize":500,"size":500}]}]}}`)

	cfg := planTestConfig(t)
	d := newOfflineDownloader(t, f.Server, cfg, newFakeConsole())

	res, err := d.InstallOptions(context.Background(), planProductID, ProductRefExact, "windows", "")
	if err != nil {
		t.Fatalf("InstallOptions: %v", err)
	}

	if len(res.Entries) != 1 {
		t.Fatalf("len(Entries) = %d, want 1", len(res.Entries))
	}

	e := res.Entries[0]
	if e.Platform != "windows" || e.Arch != "x64" || e.Language != "en-US" {
		t.Errorf("entry tuple mismatch: %+v", e)
	}
	// Size must aggregate both depots (2000 + 500 = 2500).
	if e.Size != 2500 {
		t.Errorf("entry Size = %d, want 2500", e.Size)
	}
	// Files must be 3 unique files across both depots.
	if e.Files != 3 {
		t.Errorf("entry Files = %d, want 3", e.Files)
	}
}

func TestEffectiveBuildIdentityEquality(t *testing.T) {
	// Case G: Resolver install/options build identity equality.
	f := newPlanFixture(t)
	f.setDefaultBodies()

	cfg := planTestConfig(t)
	d := newOfflineDownloader(t, f.Server, cfg, newFakeConsole())

	// 1. Call InstallOptions.
	optionsRes, err := d.InstallOptions(context.Background(), planProductID, ProductRefExact, "windows", "")
	if err != nil {
		t.Fatalf("InstallOptions: %v", err)
	}

	// 2. Call BuildPlan (install path).
	planRes, err := d.BuildPlan(context.Background(), NewInstallRequest(cfg, planProductID, "", ProductRefExact))
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}

	// 3. Directly call resolveEffectiveBuild.
	eb, err := d.resolveEffectiveBuild(context.Background(), planProductID, "", "windows")
	if err != nil {
		t.Fatalf("resolveEffectiveBuild: %v", err)
	}

	// Both paths must resolve the exact same target build identity.
	if optionsRes.BuildID != eb.BuildID {
		t.Errorf("options BuildID = %q, want %q", optionsRes.BuildID, eb.BuildID)
	}
	if optionsRes.GameTitle != eb.GameTitle {
		t.Errorf("options GameTitle = %q, want %q", optionsRes.GameTitle, eb.GameTitle)
	}

	// Verify plan succeeded with the expected tasks.
	if len(planRes.Plan.Tasks) == 0 {
		t.Errorf("plan produced 0 tasks, expected tasks from build %s", eb.BuildID)
	}

	// Test explicit buildID selection on both InstallOptions and resolveEffectiveBuild.
	optionsOld, err := d.InstallOptions(context.Background(), planProductID, ProductRefExact, "windows", "b-old")
	if err != nil {
		t.Fatalf("InstallOptions b-old: %v", err)
	}
	ebOld, err := d.resolveEffectiveBuild(context.Background(), planProductID, "b-old", "windows")
	if err != nil {
		t.Fatalf("resolveEffectiveBuild b-old: %v", err)
	}
	if optionsOld.BuildID != "b-old" || ebOld.BuildID != "b-old" {
		t.Errorf("explicit buildID mismatch: options=%q, eb=%q, want 'b-old'", optionsOld.BuildID, ebOld.BuildID)
	}
}
