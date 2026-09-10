package core

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/nekrozis/goggo/internal/model"
)

// ExtractSmallFilesContainers unpacks the plan's small-files containers: each
// member file is cut out of its container by offset and size, and the container
// is removed afterwards (downloader.cpp:4265-4311). It runs after the transfer
// has completed successfully — review D73 — because the containers are
// downloaded by transfer.Run as ordinary tasks.
//
// Member files are written without a hash check, the way upstream writes them
// (review D80). The container file itself must exist: a container whose
// download was skipped or failed is silently passed over, as the exists check
// does upstream.
func (d *Downloader) ExtractSmallFilesContainers(ctx context.Context, res PlanResult) error {
	for _, group := range res.Plan.SFC {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		container := res.InstallPath + "/" + group.Container.Path
		if _, err := os.Stat(container); err != nil {
			continue
		}

		fmt.Fprintln(d.ui.Out(), "Extracting small files container "+container)

		for _, item := range group.Items {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// Only this container's product's members live inside it
			// (downloader.cpp:4279-4281).
			if item.ProductID != group.Container.ProductID {
				continue
			}
			target := res.InstallPath + "/" + item.Path
			fmt.Fprintln(d.ui.Out(), target)

			if err := extractSFCMember(container, target, item.SFCOffset, item.SFCSize); err != nil {
				// A directory that cannot be created skips the member; the
				// container is still deleted below (downloader.cpp:4291-4296).
				fmt.Fprintln(d.ui.Out(), "Failed to create directory: "+filepath.Dir(target)+": "+err.Error())
				continue
			}
		}

		fmt.Fprintln(d.ui.Out(), "Deleting small files container "+container)
		if err := os.Remove(container); err != nil {
			fmt.Fprintln(d.ui.ErrOut(), "Failed to delete "+container)
		}
	}
	return nil
}

// extractSFCMember cuts one member out of its container: size bytes from
// offset, written to a fresh file (downloader.cpp:4299-4307). The read buffer
// has no size cap, upstream's malloc(sfc_size) equivalent.
func extractSFCMember(container, target string, offset, size uint64) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	f, err := os.Open(container)
	if err != nil {
		return err
	}
	defer f.Close()
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, io.NewSectionReader(f, int64(offset), int64(size))); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// planSFCMemberPaths lists the relative paths of every small-files member in
// the plan: the orphan check counts them as present-installed files
// (downloader.cpp:4303-4306 adds them back to the items vector).
func planSFCMemberPaths(plan model.DownloadPlan) []string {
	var paths []string
	for _, group := range plan.SFC {
		for _, item := range group.Items {
			paths = append(paths, item.Path)
		}
	}
	return paths
}
