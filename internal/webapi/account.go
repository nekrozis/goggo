package webapi

import (
	"context"

	"encoding/json/jsontext"
	"fmt"
)

// GameDetailsJSON fetches the per-game details document. A response that is not
// a JSON object is an error; the caller decides what to do with it.
func (c *Client) GameDetailsJSON(ctx context.Context, gameID string) (map[string]jsontext.Value, error) {
	return c.getResponseJSON(ctx, c.ep.www+"/account/gameDetails/"+gameID+".json")
}

// OwnedGameIDs fetches the ids of all owned products. It returns them rather
// than storing them, so the caller owns the state.
func (c *Client) OwnedGameIDs(ctx context.Context) ([]string, error) {
	root, err := c.getResponseJSON(ctx, c.ep.www+"/user/data/games")
	if err != nil {
		return nil, err
	}
	ids := []string{}
	raw := root["owned"]
	if raw.Kind() != jsontext.KindBeginArray {
		// Missing, null or not an array: no ids and no error.
		return ids, nil
	}
	owned, err := memberArray(raw)
	if err != nil {
		return nil, fmt.Errorf("webapi: owned: %w", err)
	}
	for i, el := range owned {
		s, err := memberText(el)
		if err != nil {
			return nil, fmt.Errorf("webapi: owned[%d]: %w", i, err)
		}
		ids = append(ids, s)
	}
	return ids, nil
}

// Tags fetches the account tag table.
//
// A response that is not JSON is ErrNotJSON, so the CLI can render the "cookies
// have most likely expired" hint; library layers never exit. The tags value is
// iterated as array elements in order, or as object member values: the container
// kind is not asserted.
func (c *Client) Tags(ctx context.Context) (map[string]string, error) {
	root, err := c.getResponseJSON(ctx, c.ep.www+"/account/getFilteredProducts?mediaType=1&sortBy=title&system=&page=1")
	if err != nil {
		return nil, err
	}
	tags := map[string]string{}
	raw := root["tags"]
	if raw.Kind() == jsontext.KindInvalid || raw.Kind() == jsontext.KindNull {
		return tags, nil
	}
	children, err := containerValues(raw)
	if err != nil {
		return nil, fmt.Errorf("webapi: tags: %w", err)
	}
	for i, child := range children {
		node, err := memberObject(child)
		if err != nil {
			return nil, fmt.Errorf("webapi: tags[%d]: %w", i, err)
		}
		// A missing id/name member reads as "".
		id, err := memberText(node["id"])
		if err != nil {
			return nil, fmt.Errorf("webapi: tags[%d].id: %w", i, err)
		}
		name, err := memberText(node["name"])
		if err != nil {
			return nil, fmt.Errorf("webapi: tags[%d].name: %w", i, err)
		}
		tags[id] = name
	}
	return tags, nil
}
