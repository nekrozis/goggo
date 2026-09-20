package model

import (
	"encoding/json"

	"github.com/nekrozis/goggo/internal/config"
)

// GameFile is one file of a product's details. Size is a string because GOG
// reports file sizes as strings.
//
// Filepath is the absolute local path the file is saved to; it is never
// serialised.
type GameFile struct {
	GameName              string
	ID                    string
	Name                  string
	Path                  string
	Size                  string
	Version               string
	Title                 string
	GameNameBasegame      string
	TitleBasegame         string
	GalaxyDownlinkJSONURL string
	Filepath              string

	Updated int
	Score   int
	Silent  int

	Platform uint32
	Language uint32
	Type     uint32
}

// NewGameFile returns a GameFile with the platform defaulted to Windows and
// the language to English.
func NewGameFile() GameFile {
	return GameFile{
		Platform: config.PlatformWindows,
		Language: config.LangEN,
	}
}

// MarshalJSON emits the version key only when non-empty; score and filepath are
// never emitted.
func (f GameFile) MarshalJSON() ([]byte, error) {
	obj := map[string]any{
		"updated":                  f.Updated,
		"id":                       f.ID,
		"name":                     f.Name,
		"path":                     f.Path,
		"size":                     f.Size,
		"platform":                 f.Platform,
		"language":                 f.Language,
		"silent":                   f.Silent,
		"gamename":                 f.GameName,
		"title":                    f.Title,
		"gamename_basegame":        f.GameNameBasegame,
		"title_basegame":           f.TitleBasegame,
		"type":                     f.Type,
		"galaxy_downlink_json_url": f.GalaxyDownlinkJSONURL,
	}
	if f.Version != "" {
		obj["version"] = f.Version
	}
	return json.Marshal(obj)
}
