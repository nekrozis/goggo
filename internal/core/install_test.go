package core

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"os"
	"testing"

	"github.com/nekrozis/goggo/internal/galaxy"
)

// chunkPayload is one chunk's fixture: the content the CDN serves compressed
// (with the md5 the plan's manifest must carry) and the decompressed content
// the installed file must end up with.
type chunkPayload struct {
	compressed string
	md5        string
	content    string
}

func planChunkPayload(t *testing.T, content string) chunkPayload {
	t.Helper()
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	if _, err := zw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	sum := md5.Sum(buf.Bytes())
	return chunkPayload{compressed: buf.String(), md5: hex.EncodeToString(sum[:]), content: content}
}

// TestInstallEndToEnd runs Install against the fake CDN with real chunk
// payloads: the plan resolves, the deletions execute, the transfer downloads
// and assembles, and the installed files carry the decompressed content.
//
// The depot manifests are generated around the real chunk digests — a manifest
// md5 that does not match the served bytes would fail the transfer's hash
// check, which is exactly the property this test leans on.
func TestInstallEndToEnd(t *testing.T) {
	f := newPlanFixture(t)

	base := planChunkPayload(t, "base data content")
	dlc := planChunkPayload(t, "dlc data content")
	dep := planChunkPayload(t, "dependency content")
	sfc := planChunkPayload(t, "sfc container content")

	v2New := galaxy.HashToGalaxyPath(planBuildHashNew)
	v2Old := galaxy.HashToGalaxyPath(planBuildHashOld)

	f.set("/products/"+planProductID+"/os/windows/builds",
		`{"items":[`+
			`{"build_id":"b-new","version_name":"1.0.2","date_published":"2024-03-02","generation":2,`+
			`"link":"https://cdn.gog.com/content-system/v2/meta/`+v2New+`"},`+
			`{"build_id":"b-old","version_name":"1.0.1","date_published":"2024-01-05","generation":2,`+
			`"link":"https://cdn.gog.com/content-system/v2/meta/`+v2Old+`"}]}`)

	f.set("/content-system/v2/meta/"+v2New,
		`{"baseProductId":"`+planProductID+`","installDirectory":"W3 GOTY","version":2,`+
			`"products":[{"name":"The Witcher 3: Wild Hunt"}],`+
			`"dependencies":["dep1"],`+
			`"depots":[`+
			`{"productId":"`+planProductID+`","languages":["en-US"],"osBitness":["64"],"manifest":"`+planDepotHashLang+`"},`+
			`{"productId":"`+planDLCProductID+`","languages":["en-US"],"osBitness":["64"],"manifest":"`+planDepotHashDLC+`"}]}`)

	// The base depot: the regular file, the container and the file inside it.
	// The container chunk's digest is the container file's own.
	f.set("/content-system/v2/meta/"+galaxy.HashToGalaxyPath(planDepotHashLang),
		`{"depot":{`+
			`"smallFilesContainer":{"chunks":[{"compressedMd5":"`+sfc.md5+`","size":100}]},`+
			`"items":[`+
			`{"path":"game/data.bin","md5":"base-item-md5","chunks":[{"compressedMd5":"`+base.md5+`","size":100}]},`+
			`{"path":"game/small1.txt","sfcRef":{"offset":0,"size":100},"chunks":[{"compressedMd5":"`+sfc.md5+`","size":100}]}]}}`)

	// The DLC depot re-declares game/data.bin with different content: the dedup
	// must install the DLC's bytes over the base game's.
	f.set("/content-system/v2/meta/"+galaxy.HashToGalaxyPath(planDepotHashDLC),
		`{"depot":{"items":[`+
			`{"path":"game/data.bin","md5":"dlc-item-md5","chunks":[{"compressedMd5":"`+dlc.md5+`","size":100}]}]}}`)

	f.set("/content-system/v2/meta/"+v2Old,
		`{"baseProductId":"`+planProductID+`","installDirectory":"W3 GOTY","version":2,`+
			`"depots":[{"productId":"`+planProductID+`","languages":["en-US"],"osBitness":["64"],"manifest":"`+planOldDepotHash+`"}]}`)
	f.set("/content-system/v2/meta/"+galaxy.HashToGalaxyPath(planOldDepotHash),
		`{"depot":{"items":[`+
			`{"path":"game/data.bin","md5":"base-item-md5","chunks":[{"compressedMd5":"`+base.md5+`","size":100}]},`+
			`{"path":"game/oldfile.bin","md5":"old-item-md5","chunks":[{"compressedMd5":"`+base.md5+`","size":100}]}]}}`)

	f.set("/dependencies/repository",
		`{"repository_manifest":"https://content-system.gog.com/dep/repo-manifest"}`)
	f.set("/dep/repo-manifest",
		`{"depots":[{"dependencyId":"dep1","productId":"`+planDepProductID+`",`+
			`"languages":["en-US"],"osBitness":["64"],"manifest":"`+planDepotHashDep+`"}]}`)
	f.set("/content-system/v2/dependencies/meta/"+galaxy.HashToGalaxyPath(planDepotHashDep),
		`{"depot":{"items":[`+
			`{"path":"game/dep/depfile.bin","md5":"dep-item-md5","chunks":[{"compressedMd5":"`+dep.md5+`","size":100}]}]}}`)

	// The chunk bytes the templates resolve to, keyed by the galaxy path the
	// URL carries (HashToGalaxyPath of the chunk digest).
	for _, p := range []chunkPayload{base, dlc, dep, sfc} {
		f.set("/chunks/"+galaxy.HashToGalaxyPath(p.md5), p.compressed)
	}

	cfg := planTestConfig(t)
	// The CDN priority must put the rewritten host first: the transport only
	// maps cdn.gog.com into the test server.
	cfg.DownloadConfig.GalaxyCDNPriority = []string{"cdnMain", "cdnAlt"}
	if err := os.MkdirAll(cfg.ConfigDirectory, 0o755); err != nil {
		t.Fatal(err)
	}

	// The link documents the URL provider reads during the transfer: regular
	// chunks come from secure_link with an empty path parameter, the
	// dependency's chunk path arrives on the open_link response.
	depGalaxyPath := galaxy.HashToGalaxyPath(dep.md5)
	// Each product's id gets its own secure_link call; the body is shared
	// because the template does not depend on it.
	secureLinks := `{"urls":[{"endpoint_name":"cdnAlt","url_format":"https://alt.gog.com/chunks{path}","parameters":{"path":""}},` +
		`{"endpoint_name":"cdnMain","url_format":"https://cdn.gog.com/chunks{path}","parameters":{"path":""}}]}`
	f.set("/products/"+planProductID+"/secure_link", secureLinks)
	f.set("/products/"+planDLCProductID+"/secure_link", secureLinks)
	f.set("/open_link",
		`{"urls":[{"endpoint_name":"cdnMain","url_format":"https://cdn.gog.com/chunks{path}",`+
			`"parameters":{"path":"/`+depGalaxyPath+`"}}]}`)

	// A previously installed build with a file the new build dropped: it must
	// be deleted during ApplyPlanChanges. The file inside the container is
	// pre-created on purpose — it is NOT installed by this run, so the SFC
	// decision must keep it out of the tasks while leaving it un-deleted.
	installPath := cfg.Directories.Directory + "W3 GOTY"
	if err := os.MkdirAll(installPath+"/game", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installPath+"/goggame-"+planProductID+".info",
		[]byte(`{"buildId":"b-old"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installPath+"/game/oldfile.bin", []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	console := newFakeConsole()
	d := newOfflineDownloader(t, f.Server, cfg, console)
	// The offline downloader builds an empty credential store; give it a fresh
	// token so the transfer's per-chunk expiry checks pass without a refresh.
	d.token.SetJSON(map[string]any{
		"access_token": "at", "refresh_token": "rt", "expires_in": 3600, "user_id": "u1",
	})
	if err := d.Install(context.Background(), NewInstallRequest(cfg, planProductID, "")); err != nil {
		t.Fatalf("Install: %v", err)
	}
	t.Logf("CONSOLE OUT: %q", console.out.String())
	t.Logf("CONSOLE ERR: %q", console.errOut.String())

	// The assembled files carry the decompressed content of the chunks the
	// plan selected — the DLC's bytes over the base game's for data.bin.
	assertFileContent(t, installPath+"/game/data.bin", dlc.content)
	assertFileContent(t, installPath+"/game/dep/depfile.bin", dep.content)
	assertFileContent(t, installPath+"/galaxy_smallfilescontainer_"+planProductID, sfc.content)

	// The container's member was never a download task, and the gone file was
	// removed by ApplyPlanChanges.
	assertFileAbsent(t, installPath+"/game/small1.txt")
	assertFileAbsent(t, installPath+"/game/oldfile.bin")

	out := console.out.String()
	for _, want := range []string{"The Witcher 3: Wild Hunt", "Files: 3"} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Errorf("console output missing %q", want)
		}
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(data) != want {
		t.Errorf("%s = %q, want %q", path, data, want)
	}
}

func assertFileAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		t.Errorf("%s exists, want it gone", path)
	}
}

// TestInstallRequestCarriesContext locks that Install surfaces a cancelled
// context instead of reporting a finished install.
func TestInstallRequestCarriesContext(t *testing.T) {
	f := newPlanFixture(t)
	f.setDefaultBodies()
	cfg := planTestConfig(t)
	d := newOfflineDownloader(t, f.Server, cfg, newFakeConsole())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := NewInstallRequest(cfg, planProductID, "")
	if err := d.Install(ctx, req); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the context cancellation", err)
	}
}
