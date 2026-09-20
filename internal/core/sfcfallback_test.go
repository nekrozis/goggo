package core

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/galaxy"
)

// sfcInstallFixture is the smallest install that reaches a container's
// extraction: one member that lives inside the container, whose own chunk also
// exists. containerBody is what the container file holds — passing anything
// other than the member's own content is what makes the manifest's sfcRef
// describe bytes the member does not have.
type sfcInstallFixture struct {
	f           *planFixture
	cfg         config.Config
	installPath string
	memberRel   string
	own         chunkPayload
}

// newSFCInstallFixture serves that install. serveOwnChunk controls whether the
// member's own chunk can be fetched, which is what decides the fallback's
// outcome: with it the direct download repairs the member, without it the
// install has nothing left to try.
func newSFCInstallFixture(t *testing.T, containerBody string, serveOwnChunk bool) *sfcInstallFixture {
	t.Helper()
	f := newPlanFixture(t)

	own := planChunkPayload(t, "the member's own content")
	container := planChunkPayload(t, containerBody)
	v2New := galaxy.HashToGalaxyPath(planBuildHashNew)

	f.set("/products/"+planProductID+"/os/windows/builds",
		`{"items":[{"build_id":"b-new","version_name":"1.0.2","date_published":"2024-03-02","generation":2,`+
			`"link":"https://cdn.gog.com/content-system/v2/meta/`+v2New+`"}]}`)
	f.set("/content-system/v2/meta/"+v2New,
		`{"baseProductId":"`+planProductID+`","installDirectory":"W3 GOTY","version":2,`+
			`"products":[{"name":"The Witcher 3: Wild Hunt"}],`+
			`"depots":[{"productId":"`+planProductID+`","languages":["en-US"],"osBitness":["64"],`+
			`"manifest":"`+planDepotHashLang+`"}]}`)

	// The container carries the member; the member's declared hash is the one
	// its own chunk satisfies, so a container that holds something else cannot
	// pass the extraction's check.
	f.set("/content-system/v2/meta/"+galaxy.HashToGalaxyPath(planDepotHashLang),
		`{"depot":{`+
			`"smallFilesContainer":{"chunks":[`+sfcChunkJSON(t, container)+`]},`+
			`"items":[{"path":"game/small1.txt","md5":"`+sfcMD5Hex([]byte(own.content))+`",`+
			`"sfcRef":{"offset":0,"size":`+strconv.Itoa(len(container.content))+`},`+
			`"chunks":[`+sfcChunkJSON(t, own)+`]}]}}`)

	f.set("/dependencies/repository",
		`{"repository_manifest":"https://content-system.gog.com/dep/repo-manifest"}`)
	f.set("/dep/repo-manifest", `{"depots":[]}`)

	f.set("/chunks/"+galaxy.HashToGalaxyPath(container.md5), container.compressed)
	if serveOwnChunk {
		f.set("/chunks/"+galaxy.HashToGalaxyPath(own.md5), own.compressed)
	}

	cfg := planTestConfig(t)
	cfg.DownloadConfig.GalaxyCDNPriority = []string{"cdnMain", "cdnAlt"}
	if err := os.MkdirAll(cfg.ConfigDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	f.set("/products/"+planProductID+"/secure_link",
		`{"urls":[{"endpoint_name":"cdnAlt","url_format":"https://alt.gog.com/chunks{path}","parameters":{"path":""}},`+
			`{"endpoint_name":"cdnMain","url_format":"https://cdn.gog.com/chunks{path}","parameters":{"path":""}}]}`)

	return &sfcInstallFixture{
		f:           f,
		cfg:         cfg,
		installPath: cfg.Directories.Directory + "W3 GOTY",
		memberRel:   "game/small1.txt",
		own:         own,
	}
}

// downloader hands a downloader wired to the fixture, with a token the
// transfer's expiry checks accept without a refresh.
func (fi *sfcInstallFixture) downloader(t *testing.T, console *fakeConsole) *Downloader {
	t.Helper()
	d := newOfflineDownloader(t, fi.f.Server, fi.cfg, console)
	d.token.SetJSON(map[string]any{
		"access_token": "at", "refresh_token": "rt", "expires_in": 3600, "user_id": "u1",
	})
	return d
}

// TestInstallDownloadsAContainerMemberTheContainerCannotSupply covers the
// direct-download fallback end to end: the manifest's sfcRef points the member at
// bytes that are not its own, so the extraction refuses to write them and the
// install downloads the member through the ordinary transfer instead. The first
// install then holds what the manifest declares.
func TestInstallDownloadsAContainerMemberTheContainerCannotSupply(t *testing.T) {
	fi := newSFCInstallFixture(t, "bytes that are not the member's", true)
	console := newFakeConsole()

	if err := fi.downloader(t, console).Install(context.Background(), NewInstallRequest(fi.cfg, planProductID, "", ProductRefExact)); err != nil {
		t.Fatalf("Install: %v", err)
	}

	// The member carries its own content, not the container's region.
	assertFileContent(t, fi.installPath+"/"+fi.memberRel, fi.own.content)
	// The container is gone, as every install leaves it.
	assertFileAbsent(t, fi.installPath+"/galaxy_smallfilescontainer_"+planProductID)

	out := console.out.String()
	if !strings.Contains(console.errOut.String(), "container content does not match the manifest hash") {
		t.Errorf("the refusal must be reported on the error stream: %q", console.errOut.String())
	}
	// The orphan phase ran and found nothing to report, which is the token that
	// says the refusal did not abort the install.
	if !strings.Contains(out, "\t0 orphaned files") {
		t.Errorf("the install must continue to the orphan check: %q", out)
	}
}

// TestInstallFailsWhenTheContainerMemberCannotBeDownloaded locks the failure
// side: the extraction refuses the member, the direct download cannot supply it
// either, and the install must NOT report success over the missing file.
func TestInstallFailsWhenTheContainerMemberCannotBeDownloaded(t *testing.T) {
	fi := newSFCInstallFixture(t, "bytes that are not the member's", false)
	console := newFakeConsole()

	err := fi.downloader(t, console).Install(context.Background(), NewInstallRequest(fi.cfg, planProductID, "", ProductRefExact))
	if err == nil {
		t.Fatal("Install reported success over a member that was never written")
	}
	if !strings.Contains(err.Error(), fi.memberRel) {
		t.Errorf("error = %v, want it to name the member", err)
	}
	if !strings.Contains(err.Error(), "does not match the manifest") {
		t.Errorf("error = %v, want the convergence failure", err)
	}
	// Nothing was written: the wrong bytes never reached the installation.
	assertFileAbsent(t, fi.installPath+"/"+fi.memberRel)
}

// sfcChunkJSON builds one chunk of the manifest: the compressed digest the CDN
// serves, the uncompressed digest and the sizes, all computed from content.
func sfcChunkJSON(t *testing.T, p chunkPayload) string {
	t.Helper()
	return `{"compressedMd5":"` + p.md5 + `","md5":"` + sfcMD5Hex([]byte(p.content)) + `",` +
		`"compressedSize":` + strconv.Itoa(len(p.compressed)) + `,"size":` + strconv.Itoa(len(p.content)) + `}`
}
