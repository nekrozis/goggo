package gamedetails

// GameFile mirrors one downloadable file entry (gamefile.h). The fields keep
// their upstream API names on purpose: the JSON mapping stays auditable, and
// the WebsiteTask / download-task conversions happen at the consumer layer.
type GameFile struct {
	Updated               int
	Gamename              string
	ID                    string
	Name                  string
	Path                  string
	Size                  string
	GalaxyDownlinkJSONURL string
	Version               string
	Title                 string
	GamenameBasegame      string
	TitleBasegame         string
	Platform              uint32
	Language              uint32
	Type                  uint32
	Score                 int
	Silent                int

	filepath string
}

// SetFilepath stores the derived local path (gamefile.cpp: setFilepath).
func (gf *GameFile) SetFilepath(path string) { gf.filepath = path }

// GetAsJson ports gameFile::getAsJson (gamefile.cpp): the fixed field table
// of the details-json output contract. version appears only when non-empty
// (the C++ conditional member); every other key is always present.
func (gf GameFile) GetAsJson() map[string]any {
	out := map[string]any{
		"updated":                  gf.Updated,
		"id":                       gf.ID,
		"name":                     gf.Name,
		"path":                     gf.Path,
		"size":                     gf.Size,
		"platform":                 gf.Platform,
		"language":                 gf.Language,
		"silent":                   gf.Silent,
		"gamename":                 gf.Gamename,
		"title":                    gf.Title,
		"gamename_basegame":        gf.GamenameBasegame,
		"title_basegame":           gf.TitleBasegame,
		"type":                     gf.Type,
		"galaxy_downlink_json_url": gf.GalaxyDownlinkJSONURL,
	}
	if gf.Version != "" {
		out["version"] = gf.Version
	}
	return out
}

// GetFilepath returns the derived local path (gamefile.cpp: getFilepath).
func (gf *GameFile) GetFilepath() string { return gf.filepath }
