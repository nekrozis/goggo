package core

import (
	"bufio"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/nekrozis/goggo/internal/model"
)

// ExtractSmallFilesContainers unpacks the plan's small-files containers: each
// member is cut out of its container by offset and size, and the container is
// removed afterwards. It runs after the transfer completes successfully.
//
// A member that declares a hash is verified against the bytes its region holds
// before anything is written; a mismatch is not written and comes back as a
// task to download directly, because the manifest's sfcRef cannot describe
// such a member. A container that is not on disk is passed over silently, but
// one that IS there and cannot be opened fails the run.
func (d *Downloader) ExtractSmallFilesContainers(ctx context.Context, res PlanResult) ([]model.FileTask, error) {
	var pending []model.FileTask
	for _, group := range res.Plan.SFC {
		if ctx.Err() != nil {
			return pending, ctx.Err()
		}
		container := filepath.Join(res.InstallPath, filepath.FromSlash(group.Container.Path))
		f, err := os.Open(container)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			// A permission problem, a path that is not a file, a name the
			// filesystem refuses: the members cannot be read.
			return pending, fmt.Errorf("%s: %w", container, err)
		}
		extracted, missed, err := d.extractContainer(ctx, f, group, res.InstallPath)
		f.Close()
		pending = append(pending, missed...)
		if err != nil {
			return pending, fmt.Errorf("%s: %w", container, err)
		}

		// The count is the number of members actually written; a member held
		// back for a direct download is reported on its own line instead.
		fmt.Fprintf(d.ui.Out(), "Extracting small files container %s (%d files)\n", container, extracted)

		fmt.Fprintln(d.ui.Out(), "Deleting small files container "+container)
		if err := os.Remove(container); err != nil {
			fmt.Fprintln(d.ui.ErrOut(), "Failed to delete "+container)
		}
	}
	return pending, nil
}

// extractContainer cuts the members of one group out of its container and
// reports how many files it wrote, plus the members it refused to write.
//
// The container is read once, forward: the distinct regions are visited in
// ascending offset order and the buffer keeps only the tail the next region can
// still share, which is what keeps a container from costing one read per member.
func (d *Downloader) extractContainer(ctx context.Context, src io.Reader, group model.SFCGroup, installPath string) (int, []model.FileTask, error) {
	// The verbose listing keeps the plan's order, as it did before the members
	// were grouped by region.
	for _, item := range group.Items {
		if item.ProductID != group.Container.ProductID {
			continue
		}
		if d.cfg.MsgLevel >= msgLevelVerbose {
			fmt.Fprintln(d.ui.Out(), installPath+"/"+item.Path)
		}
	}

	stream := newContainerStream(bufio.NewReaderSize(src, 64*1024))
	var (
		extracted int
		pending   []model.FileTask
	)
	for _, region := range sfcRegions(group, installPath) {
		if ctx.Err() != nil {
			return extracted, pending, ctx.Err()
		}
		data, err := stream.bytes(region.offset, region.size)
		if err != nil {
			return extracted, pending, err
		}
		// One hash per region: the members sharing it share its content.
		var sum string
		if region.declaresHash() {
			sum = sfcMD5Hex(data)
		}
		for _, member := range region.members {
			if member.item.MD5 != "" && sum != member.item.MD5 {
				fmt.Fprintln(d.ui.ErrOut(), "Failed to extract "+member.destination+
					": container content does not match the manifest hash; downloading it directly")
				pending = append(pending, model.FileTask{Item: member.item, Destination: member.destination})
				continue
			}
			if err := writeSFCMember(member.destination, data); err != nil {
				// A destination that cannot be created skips the member; the
				// container is still deleted by the caller. The gap (the file
				// stays absent) is registered.
				fmt.Fprintln(d.ui.ErrOut(), "Failed to extract "+member.destination+": "+err.Error())
				continue
			}
			extracted++
		}
	}
	return extracted, pending, nil
}

// sfcMember is one member waiting to be cut out of its container. The whole item
// travels with it: a member held back for a direct download is handed to the
// transfer as a task, and that download must use the member's own chunks, hash
// and size rather than anything derived again from the manifest.
type sfcMember struct {
	item        model.GalaxyDepotItem
	destination string
}

// sfcRegion is one distinct byte range of a container, with every member that
// claims it. Members claiming the same range share its content: the manifest
// deduplicates identical files, so the region is read and hashed once for all of
// them.
type sfcRegion struct {
	offset, size uint64
	members      []sfcMember
}

// declaresHash reports whether any member of the region carries a hash to check
// against. A region no member declares a hash for is still cut out, unverified:
// no hash means "not verifiable", never "verified".
func (r sfcRegion) declaresHash() bool {
	for _, member := range r.members {
		if member.item.MD5 != "" {
			return true
		}
	}
	return false
}

// sfcRegions groups a group's members by the range they claim, in ascending
// offset order. Members of another product do not live in this container and are
// left out.
func sfcRegions(group model.SFCGroup, installPath string) []sfcRegion {
	byRange := map[[2]uint64][]sfcMember{}
	for _, item := range group.Items {
		if item.ProductID != group.Container.ProductID {
			continue
		}
		key := [2]uint64{item.SFCOffset, item.SFCSize}
		byRange[key] = append(byRange[key], sfcMember{
			item:        item,
			destination: filepath.Join(installPath, filepath.FromSlash(item.Path)),
		})
	}

	regions := make([]sfcRegion, 0, len(byRange))
	for key, members := range byRange {
		regions = append(regions, sfcRegion{offset: key[0], size: key[1], members: members})
	}
	sort.Slice(regions, func(i, j int) bool {
		if regions[i].offset != regions[j].offset {
			return regions[i].offset < regions[j].offset
		}
		return regions[i].size < regions[j].size
	})
	return regions
}

// containerStream serves regions of one container in ascending offset order from
// a single forward pass. The buffer holds the source bytes from bufStart on, so a
// region that starts inside the buffered tail — the container packs unique blobs
// and a member may point inside another member's range — is answered without
// touching the file again.
type containerStream struct {
	src      io.Reader
	buf      []byte
	bufStart uint64
	// eof records that the source is spent, so a region past the end is
	// answered short without probing the reader again.
	eof bool
}

func newContainerStream(src io.Reader) *containerStream { return &containerStream{src: src} }

// bytes returns the region [offset, offset+size). A region that runs past the
// end of the container comes back short: what the container does not hold cannot
// be cut out of it, and whether that is a problem is the hash check's answer, not
// this reader's.
func (s *containerStream) bytes(offset, size uint64) ([]byte, error) {
	if offset < s.bufStart {
		return nil, fmt.Errorf("region at %d is behind the read position %d", offset, s.bufStart)
	}
	if drop := offset - s.bufStart; drop > 0 {
		if drop >= uint64(len(s.buf)) {
			// The region starts past everything buffered: read the gap away so
			// the next bytes read are the ones the region asks for.
			gap := offset - (s.bufStart + uint64(len(s.buf)))
			s.buf = s.buf[:0]
			s.bufStart = offset
			if gap > 0 {
				if _, err := io.CopyN(io.Discard, s.src, int64(gap)); err != nil && err != io.EOF {
					return nil, err
				}
			}
		} else {
			s.buf = append(s.buf[:0], s.buf[drop:]...)
			s.bufStart = offset
		}
	}
	if s.eof {
		return s.short(size), nil
	}
	for uint64(len(s.buf)) < size {
		chunk := make([]byte, 64*1024)
		n, err := s.src.Read(chunk)
		if n > 0 {
			s.buf = append(s.buf, chunk[:n]...)
		}
		if err != nil {
			if err == io.EOF {
				s.eof = true
				break
			}
			return nil, err
		}
		if n == 0 {
			return nil, io.ErrNoProgress
		}
	}
	return s.short(size), nil
}

// short is the region, clamped to what has been read.
func (s *containerStream) short(size uint64) []byte {
	if uint64(len(s.buf)) > size {
		return s.buf[:size]
	}
	return s.buf
}

// writeSFCMember writes one extracted member: its directories first, then the
// region in a single write.
func writeSFCMember(target string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.WriteFile(target, data, 0o644)
}

// sfcMD5Hex is the hash a member's content is checked against.
func sfcMD5Hex(data []byte) string {
	sum := md5.Sum(data)
	return hex.EncodeToString(sum[:])
}
