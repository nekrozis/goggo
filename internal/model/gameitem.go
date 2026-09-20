package model

import "encoding/json/jsontext"

// GameItem is one account product as it comes out of the website listing,
// before the per-game details are expanded into a GameDetails.
type GameItem struct {
	// DLCNames holds the distinct DLC names found in the game details
	// ("dlcs" subtree) when DLC information was requested.
	DLCNames []string

	// Name is the product slug; ID is the GOG product id.
	Name string
	ID   string

	// GameDetailsJSON is the raw per-game details document, nil when it was
	// not requested or the request failed.
	GameDetailsJSON map[string]jsontext.Value

	// Updates is the product's update count.
	Updates int

	// IsNew is the product's "isNew" flag.
	IsNew bool
}
