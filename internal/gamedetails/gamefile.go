package gamedetails

// GameFile is one downloadable file entry. The fields keep the API's names so
// the JSON mapping stays auditable; the WebsiteTask and download-task
// conversions happen at the consumer layer.
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

// SetFilepath stores the derived local path.
func (gf *GameFile) SetFilepath(path string) { gf.filepath = path }

// GetAsJson returns the fixed field table of the details-json output contract.
// version appears only when non-empty; every other key is always present.
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

// GetFilepath returns the derived local path.
func (gf *GameFile) GetFilepath() string { return gf.filepath }
