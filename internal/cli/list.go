package cli

import (
	"context"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/nekrozis/goggo/internal/catalog"
	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/model"
	"github.com/nekrozis/goggo/internal/util"
)

// ansii colour codes used by the game listing (downloader.cpp:525-532).
const (
	ansiNewGame  = "\033[01;34m"
	ansiUpdated  = "\033[32m"
	ansiReset    = "\033[0m"
	dlcLineStart = "+> "
)

// renderList fetches and prints one of the list formats this build supports.
// The data always comes from internal/catalog (or webapi for the tag table);
// the CLI never re-implements filtering or mapping here.
func renderList(ctx context.Context, s *Session, inv Invocation, w io.Writer) error {
	switch inv.ListFormat {
	case config.ListFormatGames:
		res, err := catalog.List(ctx, s.Web, catalog.ListOptions{
			Tags:              s.Config.DownloadConfig.Tags,
			GameRegex:         s.Config.GameRegex,
			FilterListPath:    s.Config.GameListFilePath,
			IgnoreDLCCountRE:  s.Config.IgnoreDLCCountRegex,
			InstallerPlatform: s.Config.DownloadConfig.InstallerPlatform,
			Include:           s.Config.DownloadConfig.Include,
			Updated:           s.Config.Updated,
			NewOnly:           s.Config.New,
			IncludeHidden:     s.Config.IncludeHiddenProducts,
			PlatformDetection: s.Config.PlatformDetection,
			UpdateCache:       s.Config.UpdateCache,
		})
		if err != nil {
			return err
		}
		return renderGames(w, res.Games, s.Config.Color)
	case config.ListFormatTags:
		tags, err := s.Web.Tags(ctx)
		if err != nil {
			return err
		}
		return renderTags(w, tags)
	case config.ListFormatWishlist:
		items, err := catalog.Wishlist(ctx, s.Web, catalog.WishlistOptions{
			InstallerPlatform: s.Config.DownloadConfig.InstallerPlatform,
			PlatformDetection: s.Config.PlatformDetection,
		})
		if err != nil {
			return err
		}
		return renderWishlist(w, items)
	default:
		return fmt.Errorf("list format not implemented")
	}
}

// renderGames mirrors Downloader::listGames for LIST_FORMAT_GAMES
// (downloader.cpp:507-538): the name, an update counter in brackets when there
// are updates, the new-game colouring when colours are on, then one "+> " line
// per DLC name.
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

// renderTags mirrors the LIST_FORMAT_TAGS branch (downloader.cpp:540-552). The
// C++ source iterates a std::map, so the output is ordered by tag id.
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

// renderWishlist mirrors Downloader::showWishlist (downloader.cpp:2530-2565).
//
// The release date is rendered in UTC: the C++ value goes through
// boost::posix_time::from_time_t, which is defined as the epoch plus the given
// seconds with no timezone conversion (boost date_time conversion.hpp).
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
