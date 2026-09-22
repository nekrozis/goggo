package model

// GameItem is one account product as it comes out of the website listing,
// before the per-game details are expanded into a GameDetails.
type GameItem struct {
	// DLCNames holds the distinct DLC names found in the game details
	// ("dlcs" subtree) when DLC information was requested.
	DLCNames []string `json:"dlcNames,omitempty"`

	// Name is the product slug; ID is the GOG product id.
	Name string `json:"name"`
	ID   string `json:"id"`

	// GameDetailsJSON is the raw per-game details document, nil when it was
	// not requested or the request failed.
	GameDetailsJSON []byte `json:"-"`

	// Updates is the product's update count.
	Updates int `json:"updates"`

	// IsNew is the product's "isNew" flag.
	IsNew bool `json:"isNew"`
}
