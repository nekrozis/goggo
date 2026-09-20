package transfer

import (
	"context"
	"errors"

	"github.com/nekrozis/goggo/internal/httpx"
	"github.com/nekrozis/goggo/internal/model"
)

// ArtifactDeps is what the direct artifact download needs from outside: the
// HTTP client - cookies, retry policy and the low-speed guard travel with it -
// and nothing else. A logo or icon URL is complete on its own; it is not a
// Galaxy downlink, which is why this path exists beside WebsiteTask instead
// of being modeled as one.
type ArtifactDeps struct {
	HTTP *httpx.Client
}

// DownloadArtifact fetches one complete URL onto destination through the SAME
// attempt loop the website download runs: retry classification, failure cleanup (a
// transport break keeps the partial, anything else removes it) and the server
// timestamp moved onto the file. There is deliberately no exists-skip: a logo or
// icon is re-fetched every run. The emit sink is a no-op — an image is not worth a
// progress surface; the caller reports the outcome as a structured artifact.
func DownloadArtifact(ctx context.Context, url, destination string, opts Options, deps ArtifactDeps) error {
	if deps.HTTP == nil {
		return errors.New("transfer: artifact download needs an http client")
	}
	task := model.WebsiteTask{Destination: destination}
	return downloadWithRetries(ctx, task, opts, WebsiteDeps{HTTP: deps.HTTP}, url, false, func(Event) {})
}
