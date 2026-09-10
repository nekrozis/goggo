package model

// GameItem mirrors struct gameItem (util.h:35-43): one account product as it
// comes out of the website listing, before the per-game details are expanded
// into a GameDetails.
//
// Differences from the C++ struct (intentional): GameDetailsJSON is a decoded
// JSON object instead of a Json::Value, and the field order below follows the
// project layout rule rather than the original declaration order.
//
// Fields are ordered to minimise padding: the slice (24B), the strings (16B
// each), the map and int (8B each), then the bool.
type GameItem struct {
	// DLCNames holds the distinct DLC names found in the game details
	// ("dlcs" subtree) when DLC information was requested.
	DLCNames []string

	// Name is the product slug; ID is the GOG product id (website.cpp:175-176).
	Name string
	ID   string

	// GameDetailsJSON is the raw per-game details document, nil when it was
	// not requested or the request failed.
	GameDetailsJSON map[string]any

	// Updates is the product's update count (website.cpp:179-202).
	Updates int

	// IsNew mirrors the product's "isNew" flag.
	IsNew bool
}
