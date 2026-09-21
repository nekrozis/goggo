package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/nekrozis/goggo/internal/model"
)

func renderProductInfo(w io.Writer, p model.ProductInfo) {
	fmt.Fprintf(w, "%-14s%s\n", "Title:", p.Title)
	fmt.Fprintf(w, "%-14s%s\n", "Product ID:", p.ID)
	if p.Slug != "" {
		fmt.Fprintf(w, "%-14s%s\n", "Slug:", p.Slug)
	}
	if len(p.Platforms) != 0 {
		fmt.Fprintf(w, "%-14s%s\n", "Platforms:", strings.Join(p.Platforms, ", "))
	}
	if p.Icon != "" {
		fmt.Fprintf(w, "%-14s%s\n", "Icon:", p.Icon)
	}
	if p.Logo != "" {
		fmt.Fprintf(w, "%-14s%s\n", "Logo:", p.Logo)
	}
	if p.ReleaseDate != "" {
		fmt.Fprintf(w, "%-14s%s\n", "Release date:", p.ReleaseDate)
	}

	tagsGenres := combineTagsGenres(p.Tags, p.Genres)
	if len(tagsGenres) != 0 {
		fmt.Fprintf(w, "%-14s%s\n", "Tags/Genres:", strings.Join(tagsGenres, ", "))
	}

	if len(p.DLCs) != 0 {
		dlcTitles := make([]string, 0, len(p.DLCs))
		for _, dlc := range p.DLCs {
			if dlc.Title != "" {
				dlcTitles = append(dlcTitles, dlc.Title)
			} else if dlc.Slug != "" {
				dlcTitles = append(dlcTitles, dlc.Slug)
			} else if dlc.ID != "" {
				dlcTitles = append(dlcTitles, dlc.ID)
			}
		}
		if len(dlcTitles) != 0 {
			fmt.Fprintf(w, "%-14s%s\n", "DLCs:", strings.Join(dlcTitles, ", "))
		}
	}

	if p.Description != "" {
		fmt.Fprintf(w, "%-14s%s\n", "Description:", p.Description)
	}
}

func combineTagsGenres(tags, genres []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t != "" && !seen[strings.ToLower(t)] {
			seen[strings.ToLower(t)] = true
			out = append(out, t)
		}
	}
	for _, g := range genres {
		g = strings.TrimSpace(g)
		if g != "" && !seen[strings.ToLower(g)] {
			seen[strings.ToLower(g)] = true
			out = append(out, g)
		}
	}
	return out
}
