package core

import (
	"reflect"
	"testing"

	"github.com/nekrozis/goggo/internal/model"
)

func TestParseProductInfoFull(t *testing.T) {
	fixture := []byte(`{
		"id": 1207658991,
		"title": "Heroes of Might and Magic 3 Complete",
		"slug": "heroes_of_might_and_magic_3_complete_edition",
		"release_date": "1999-06-01T00:00:00+02:00",
		"content_system_compatibility": {
			"windows": true,
			"osx": false,
			"linux": false
		},
		"images": {
			"icon": "//images.gog.com/icon.png",
			"logo": "//images.gog.com/logo_glx_logo.jpg"
		},
		"description": {
			"lead": "A classic strategy game.<br>Lead your army to victory."
		},
		"tags": ["Strategy", "Turn-based"],
		"genres": [{"name": "Fantasy"}],
		"expanded_dlcs": [
			{"id": 101, "slug": "armageddons_blade", "title": "Armageddon's Blade"},
			{"id": "102", "slug": "shadow_of_death", "title": "The Shadow of Death"}
		]
	}`)

	info, err := parseProductInfo(fixture)
	if err != nil {
		t.Fatalf("parseProductInfo failed: %v", err)
	}

	if info.ID != "1207658991" {
		t.Errorf("ID = %q, want %q", info.ID, "1207658991")
	}
	if info.Title != "Heroes of Might and Magic 3 Complete" {
		t.Errorf("Title = %q", info.Title)
	}
	if info.Slug != "heroes_of_might_and_magic_3_complete_edition" {
		t.Errorf("Slug = %q", info.Slug)
	}
	if info.ReleaseDate != "1999-06-01" {
		t.Errorf("ReleaseDate = %q, want %q", info.ReleaseDate, "1999-06-01")
	}
	if !reflect.DeepEqual(info.Platforms, []string{"Windows"}) {
		t.Errorf("Platforms = %v, want [Windows]", info.Platforms)
	}
	if info.Icon != "https://images.gog.com/icon.png" {
		t.Errorf("Icon = %q, want https://images.gog.com/icon.png", info.Icon)
	}
	if info.Logo != "https://images.gog.com/logo.jpg" {
		t.Errorf("Logo = %q, want https://images.gog.com/logo.jpg", info.Logo)
	}
	wantDesc := "A classic strategy game.\nLead your army to victory."
	if info.Description != wantDesc {
		t.Errorf("Description = %q, want %q", info.Description, wantDesc)
	}
	if !reflect.DeepEqual(info.Tags, []string{"Strategy", "Turn-based"}) {
		t.Errorf("Tags = %v", info.Tags)
	}
	if !reflect.DeepEqual(info.Genres, []string{"Fantasy"}) {
		t.Errorf("Genres = %v", info.Genres)
	}
	wantDLCs := []model.DLCInfo{
		{ID: "101", Slug: "armageddons_blade", Title: "Armageddon's Blade"},
		{ID: "102", Slug: "shadow_of_death", Title: "The Shadow of Death"},
	}
	if !reflect.DeepEqual(info.DLCs, wantDLCs) {
		t.Errorf("DLCs = %+v, want %+v", info.DLCs, wantDLCs)
	}
}

func TestParseProductInfoMinimalAndNulls(t *testing.T) {
	fixture := []byte(`{
		"id": "555",
		"title": "Minimal Game",
		"slug": "minimal_game",
		"release_date": null,
		"content_system_compatibility": null,
		"platforms": {
			"windows": true,
			"osx": true,
			"linux": true
		},
		"images": null,
		"description": "Simple string description with <b>HTML</b> tags.",
		"tags": null,
		"genres": null,
		"expanded_dlcs": null
	}`)

	info, err := parseProductInfo(fixture)
	if err != nil {
		t.Fatalf("parseProductInfo failed: %v", err)
	}

	if info.ID != "555" || info.Title != "Minimal Game" || info.Slug != "minimal_game" {
		t.Errorf("basic fields mismatch: %+v", info)
	}
	if info.ReleaseDate != "" {
		t.Errorf("ReleaseDate = %q, want empty", info.ReleaseDate)
	}
	if !reflect.DeepEqual(info.Platforms, []string{"Windows", "Mac", "Linux"}) {
		t.Errorf("Platforms = %v, want [Windows Mac Linux]", info.Platforms)
	}
	if info.Icon != "" || info.Logo != "" {
		t.Errorf("Images = %q, %q, want empty", info.Icon, info.Logo)
	}
	if info.Description != "Simple string description with HTML tags." {
		t.Errorf("Description = %q", info.Description)
	}
	if len(info.Tags) != 0 || len(info.Genres) != 0 || len(info.DLCs) != 0 {
		t.Errorf("collections should be empty: tags=%v, genres=%v, dlcs=%v", info.Tags, info.Genres, info.DLCs)
	}
}

func TestParseProductInfoEmptyBody(t *testing.T) {
	_, err := parseProductInfo(nil)
	if err == nil {
		t.Error("expected error on empty body, got nil")
	}
	_, err = parseProductInfo([]byte("{}"))
	if err != nil {
		t.Errorf("valid empty object should unmarshal: %v", err)
	}
}
