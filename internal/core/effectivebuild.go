package core

import (
	"context"
	"fmt"
	"strings"
)

// EffectiveBuild is the resolved target Galaxy build and its associated manifest.
type EffectiveBuild struct {
	Manifest   map[string]any
	ProductID  string
	Platform   string
	BuildID    string
	BuildHash  string
	Link       string
	GameTitle  string
	Items      []any
	Notices    []string
	Generation int
	Index      int
}

// resolveEffectiveBuild is the single authoritative build resolver shared by
// installation planning and installation options discovery.
//
// It queries the builds document for the product and platform, applies sort
// ordering, resolves the target build index (defaulting to index 0 if unspecified),
// validates generation 2 compatibility, and fetches the generation 2 manifest.
func (d *Downloader) resolveEffectiveBuild(ctx context.Context, productID, buildID, platform string) (*EffectiveBuild, error) {
	builds, err := d.galaxy.ProductBuilds(ctx, productID, platform, "")
	if err != nil {
		return nil, err
	}
	builds, err = d.sortProductBuilds(builds)
	if err != nil {
		return nil, err
	}

	items, err := buildsItems(builds)
	if err != nil {
		return nil, err
	}
	if len(builds) == 0 && platform == platformLinux {
		return &EffectiveBuild{
			ProductID: productID,
			Platform:  platform,
			Notices:   []string{msgNoLinuxSupport, msgCheckInstallers},
		}, fmt.Errorf("linux installer fallback: %w", ErrNotImplemented)
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("no Galaxy builds found for product %s on platform %s", productID, platform)
	}

	index, err := buildIndexFor(items, buildID)
	if err != nil {
		return nil, err
	}
	if index < 0 {
		index = 0
	}

	generation := 0
	resolvedBuildID := ""
	if index < len(items) {
		entry, err := mapObject(items[index])
		if err != nil {
			return nil, fmt.Errorf("galaxy: builds items[%d]: %w", index, err)
		}
		gen, err := intValue(entry["generation"])
		if err != nil {
			return nil, fmt.Errorf("galaxy: builds items[%d].generation: %w", index, err)
		}
		generation = int(gen)
		if bid, err := scalarString(entry["build_id"]); err == nil {
			resolvedBuildID = bid
		}
	}
	if generation != 2 {
		return &EffectiveBuild{
			ProductID:  productID,
			Platform:   platform,
			BuildID:    resolvedBuildID,
			Index:      index,
			Generation: generation,
			Notices:    []string{msgGenerationsOneTwo},
		}, nil
	}

	link, err := buildLink(items, index)
	if err != nil {
		return nil, err
	}
	buildHash := link[strings.LastIndexByte(link, '/')+1:]

	manifest, err := d.galaxy.ManifestV2(ctx, buildHash, false)
	if err != nil {
		return nil, err
	}
	gameTitle := manifestProductName(manifest)

	return &EffectiveBuild{
		ProductID:  productID,
		Platform:   platform,
		BuildID:    resolvedBuildID,
		BuildHash:  buildHash,
		Generation: generation,
		Link:       link,
		Index:      index,
		Items:      items,
		Manifest:   manifest,
		GameTitle:  gameTitle,
	}, nil
}
