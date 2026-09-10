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

// GetFilepath returns the derived local path (gamefile.cpp: getFilepath).
func (gf *GameFile) GetFilepath() string { return gf.filepath }
