package core

import (
	"context"
	"errors"
	"sort"

	"github.com/nekrozis/goggo/internal/galaxy"
	"github.com/nekrozis/goggo/internal/model"
)

// InstallOptionEntry describes one available (Platform, Arch, Language) installation option.
type InstallOptionEntry struct {
	Platform string `json:"platform"`
	Arch     string `json:"arch"`
	Language string `json:"language"`
	Size     int64  `json:"size"`
	Files    int    `json:"files"`
}

// DLCOption is one DLC of the selected build as the listing presents it: the
// product id --dlc accepts, the title the product document carries, and whether
// the account owns it.
//
// The product id is the fact the selection works from; a title is only how a
// user names it, so an unreadable product document leaves the title empty
// rather than dropping the DLC from the listing.
type DLCOption struct {
	ProductID string `json:"product_id"`
	Title     string `json:"title"`
	Owned     bool   `json:"owned"`
}

// InstallOptionsResult is the aggregated result of an install options inquiry.
type InstallOptionsResult struct {
	GameTitle string               `json:"game_title"`
	BuildID   string               `json:"build_id"`
	Entries   []InstallOptionEntry `json:"options"`
	// DLCs are the DLC products the selected build carries. The listing and the
	// plan's selectors read the same discovery, so a product id shown here is
	// exactly one --dlc accepts.
	DLCs []DLCOption `json:"dlcs"`
}

// InstallOptions resolves the game reference and discovers all compatible installation
// options for the effective build on the specified platform.
//
// It queries the single authoritative effective-build resolver, inspects the primary
// base content depots in the build manifest, excludes support metadata and DLC depots,
// and aggregates content by (Platform, Arch, Language) tuples.
func (d *Downloader) InstallOptions(ctx context.Context, ref string, mode ProductRefMode, platform, buildID string) (InstallOptionsResult, error) {
	id, notice, err := d.selectProductID(ctx, ref, mode)
	if err != nil {
		return InstallOptionsResult{}, err
	}
	if id == "" {
		text := notice.Text
		if text == "" {
			text = msgNoProducts
		}
		return InstallOptionsResult{}, errors.New(text)
	}

	eb, err := d.resolveEffectiveBuild(ctx, id, buildID, platform)
	if err != nil {
		return InstallOptionsResult{}, err
	}
	if eb.Generation != 2 {
		return InstallOptionsResult{}, errors.New(msgGenerationsOneTwo)
	}

	baseProductID, _ := documentString(eb.Manifest, "baseProductId")
	if baseProductID == "" {
		baseProductID = id
	}

	// The DLC section is reported even when no base tuple matches: it answers a
	// question about the build, not about this platform's content.
	dlcs, err := d.dlcListing(ctx, eb.Manifest, baseProductID)
	if err != nil {
		return InstallOptionsResult{}, err
	}

	candidates, err := depotCandidates(eb.Manifest, baseProductID)
	if err != nil {
		return InstallOptionsResult{}, err
	}

	if len(candidates) == 0 {
		return InstallOptionsResult{
			GameTitle: eb.GameTitle,
			BuildID:   eb.BuildID,
			Entries:   nil,
			DLCs:      dlcs,
		}, nil
	}

	// Determine distinct candidate languages across all base depots.
	distinctLangs := make(map[string]struct{})
	for _, c := range candidates {
		for _, l := range c.languages {
			if l != "*" {
				distinctLangs[l] = struct{}{}
			}
		}
	}
	hasSpecificLangs := len(distinctLangs) > 0
	if !hasSpecificLangs {
		distinctLangs["*"] = struct{}{}
	}

	// Determine supported architectures across all base depots.
	hasX86 := false
	hasX64 := false
	for _, c := range candidates {
		if len(c.osBitness) == 0 {
			hasX86 = true
			hasX64 = true
			continue
		}
		for _, b := range c.osBitness {
			if b == "32" || b == "*" {
				hasX86 = true
			}
			if b == "64" || b == "*" {
				hasX64 = true
			}
		}
	}

	var arches []string
	if hasX64 {
		arches = append(arches, "x64")
	}
	if hasX86 {
		arches = append(arches, "x86")
	}

	sortedLangs := make([]string, 0, len(distinctLangs))
	for l := range distinctLangs {
		sortedLangs = append(sortedLangs, l)
	}
	sort.Strings(sortedLangs)

	depotOpts := galaxy.DepotOptions{
		LowercasePaths: d.cfg.DownloadConfig.GalaxyLowercasePath,
		Platform:       d.cfg.DownloadConfig.GalaxyPlatform,
	}

	// Cache expanded depot items by manifest hash to avoid duplicate fetches.
	depotItemsCache := make(map[string][]model.GalaxyDepotItem)
	getDepotItems := func(hash string) ([]model.GalaxyDepotItem, error) {
		if cached, ok := depotItemsCache[hash]; ok {
			return cached, nil
		}
		items, err := d.galaxy.DepotItems(ctx, hash, depotOpts)
		if err != nil {
			return nil, err
		}
		depotItemsCache[hash] = items
		return items, nil
	}

	var entries []InstallOptionEntry

	for _, arch := range arches {
		for _, lang := range sortedLangs {
			// Find depots matching this (platform, arch, lang) tuple.
			var matchedDepots []depotCandidate
			hasExplicitLangMatch := false

			for _, c := range candidates {
				// Arch match check.
				archMatches := false
				if len(c.osBitness) == 0 {
					archMatches = true
				} else {
					for _, b := range c.osBitness {
						if b == "*" || (arch == "x86" && b == "32") || (arch == "x64" && b == "64") {
							archMatches = true
							break
						}
					}
				}
				if !archMatches {
					continue
				}

				// Language match check.
				langMatches := false
				for _, l := range c.languages {
					if l == lang {
						langMatches = true
						hasExplicitLangMatch = true
						break
					} else if l == "*" {
						langMatches = true
					}
				}
				if langMatches {
					matchedDepots = append(matchedDepots, c)
				}
			}

			// If the game has language-specific depots, at least one matched depot
			// must explicitly declare the target language.
			if hasSpecificLangs && !hasExplicitLangMatch {
				continue
			}
			if len(matchedDepots) == 0 {
				continue
			}

			// Aggregate size and files for this tuple.
			// Each depot contributes its size at most once.
			var totalSize int64
			uniquePaths := make(map[string]struct{})
			var fallbackSize int64

			for _, md := range matchedDepots {
				totalSize += md.size

				items, err := getDepotItems(md.manifestHash)
				if err != nil {
					return InstallOptionsResult{}, err
				}
				for _, it := range items {
					if _, seen := uniquePaths[it.Path]; !seen {
						uniquePaths[it.Path] = struct{}{}
						for _, chunk := range it.Chunks {
							fallbackSize += int64(chunk.Size)
						}
					}
				}
			}

			if totalSize == 0 {
				totalSize = fallbackSize
			}

			if len(uniquePaths) == 0 {
				continue
			}

			entries = append(entries, InstallOptionEntry{
				Platform: eb.Platform,
				Arch:     arch,
				Language: lang,
				Size:     totalSize,
				Files:    len(uniquePaths),
			})
		}
	}

	// Sort entries by Platform, Arch, Language.
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Platform != entries[j].Platform {
			return entries[i].Platform < entries[j].Platform
		}
		if entries[i].Arch != entries[j].Arch {
			return entries[i].Arch < entries[j].Arch
		}
		return entries[i].Language < entries[j].Language
	})

	return InstallOptionsResult{
		GameTitle: eb.GameTitle,
		BuildID:   eb.BuildID,
		Entries:   entries,
		DLCs:      dlcs,
	}, nil
}
