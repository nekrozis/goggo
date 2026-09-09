package model

import (
	"encoding/json"

	"github.com/nekrozis/goggo/internal/config"
)

// GameFile mirrors class gameFile (include/gamefile.h:17-44). Field
// semantics follow the original names, e.g. Size is a string because GOG
// reports file sizes as strings.
//
// Filepath mirrors the C++ private member with its setFilepath/getFilepath
// accessors collapsed into one exported field (see setFilepath in
// src/gamefile.cpp:23-31).
type GameFile struct {
	Updated               int
	GameName              string
	ID                    string
	Name                  string
	Path                  string
	Size                  string
	Version               string
	Title                 string
	Platform              uint32
	Language              uint32
	Type                  uint32
	Score                 int
	Silent                int
	GalaxyDownlinkJSONURL string
	Filepath              string

	// Base-game provenance for DLC items (gamefile.h:30-31).
	GameNameBasegame string
	TitleBasegame    string
}

// NewGameFile mirrors the default constructor (src/gamefile.cpp:9-16):
// platform defaults to Windows and language to English.
func NewGameFile() GameFile {
	return GameFile{
		Platform: config.PlatformWindows,
		Language: config.LangEN,
	}
}

// MarshalJSON mirrors gameFile::getAsJson (src/gamefile.cpp:33-55): the
// version key is emitted only when non-empty; score and filepath are never
// emitted.
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
