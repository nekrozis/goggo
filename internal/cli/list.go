package cli

import (
	"context"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/nekrozis/goggo/internal/catalog"
	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/core"
	"github.com/nekrozis/goggo/internal/model"
	"github.com/nekrozis/goggo/internal/util"
)

// ansii colour codes used by the game listing.
const (
	ansiNewGame  = "\033[01;34m"
	ansiUpdated  = "\033[32m"
	ansiReset    = "\033[0m"
	dlcLineStart = "+> "
)

// renderList fetches and prints one of the list formats this build supports.
// The data always comes from internal/catalog (or webapi for the tag table); the
// CLI never re-implements filtering or mapping here. The listing command was
// deliberately left out of internal/core (D17), so the orchestration it needs is
// reached through the two accessors below.
func renderList(ctx context.Context, d *core.Downloader, format uint32, w io.Writer) error {
	cfg := d.Config()
	web := d.Web()
	switch format {
	case config.ListFormatGames:
		res, err := catalog.List(ctx, web, catalog.ListOptions{
			Tags:              cfg.DownloadConfig.Tags,
			GameRegex:         cfg.GameRegex,
			FilterListPath:    cfg.GameListFilePath,
			IgnoreDLCCountRE:  cfg.IgnoreDLCCountRegex,
			InstallerPlatform: cfg.DownloadConfig.InstallerPlatform,
			Include:           cfg.DownloadConfig.Include,
			Updated:           cfg.Updated,
			NewOnly:           cfg.New,
			IncludeHidden:     cfg.IncludeHiddenProducts,
			PlatformDetection: cfg.PlatformDetection,
			UpdateCache:       cfg.UpdateCache,
		})
		if err != nil {
			return err
		}
		return renderGames(w, res.Games, cfg.Color)
	case config.ListFormatTags:
		tags, err := web.Tags(ctx)
		if err != nil {
			return err
		}
		return renderTags(w, tags)
	case config.ListFormatWishlist:
		items, err := catalog.Wishlist(ctx, web, catalog.WishlistOptions{
			InstallerPlatform: cfg.DownloadConfig.InstallerPlatform,
			PlatformDetection: cfg.PlatformDetection,
		})
		if err != nil {
			return err
		}
		return renderWishlist(w, items)
	default:
		return fmt.Errorf("list format not implemented")
	}
}

// renderGames prints the games listing: the name, an update counter in brackets
// when there are updates, the new-game colouring when colours are on, then one
// "+> " line per DLC name.
func renderGames(w io.Writer, items []model.GameItem, color bool) error {
	for _, item := range items {
		name := item.Name
		switch {
		case item.Updates > 0:
			name = fmt.Sprintf("%s [%d]", name, item.Updates)
			if color {
				code := ansiUpdated
				if item.IsNew {
					code = ansiNewGame
				}
				name = code + name + ansiReset
			}
		case color && item.IsNew:
			name = ansiNewGame + name + ansiReset
		}
		if _, err := fmt.Fprintln(w, name); err != nil {
			return err
		}
		for _, dlc := range item.DLCNames {
			if _, err := fmt.Fprintln(w, dlcLineStart+dlc); err != nil {
				return err
			}
		}
	}
	return nil
}

// renderTags prints the tag table, ordered by tag id.
func renderTags(w io.Writer, tags map[string]string) error {
	ids := make([]string, 0, len(tags))
	for id := range tags {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if _, err := fmt.Fprintf(w, "%s = %s\n", id, tags[id]); err != nil {
			return err
		}
	}
	return nil
}

// renderWishlist prints the wishlist.
//
// The release date is rendered in UTC: the stored value is the epoch plus the
// given seconds, with no timezone conversion.
func renderWishlist(w io.Writer, items []model.WishlistItem) error {
	for _, item := range items {
		tags := ""
		for i, tag := range item.Tags {
			if i > 0 {
				tags += ", "
			}
			tags += tag
		}
		if tags != "" {
			tags = " [" + tags + "]"
		}

		price := item.Price
		if item.IsDiscounted {
			price += " (-" + item.DiscountPercent + " | -" + item.Discount + ")"
		}

		if _, err := fmt.Fprintf(w, "%s%s\n", item.Title, tags); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "\t%s\n", item.URL); err != nil {
			return err
		}
		if item.Platform != 0 {
			platforms := util.OptionNameString(item.Platform, config.Platforms)
			if _, err := fmt.Fprintf(w, "\tPlatforms: %s\n", platforms); err != nil {
				return err
			}
		}
		if item.ReleaseDateTime != 0 {
			when := time.Unix(item.ReleaseDateTime, 0).UTC().Format("2006-Jan-02 15:04:05")
			if _, err := fmt.Fprintf(w, "\tRelease date: %s\n", when); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "\tPrice: %s\n", price); err != nil {
			return err
		}
		if item.IsBonusStoreCreditIncluded {
			if _, err := fmt.Fprintf(w, "\tStore credit: %s\n", item.StoreCredit); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}
	return nil
}
