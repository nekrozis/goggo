package webapi

import (
	"context"
	"fmt"

	"github.com/nekrozis/goggo/internal/jsonval"
)

// GameDetailsJSON fetches the per-game details document. A response that is not
// a JSON object is an error; the caller decides what to do with it.
func (c *Client) GameDetailsJSON(ctx context.Context, gameID string) (map[string]any, error) {
	return c.getResponseJSON(ctx, c.ep.www+"/account/gameDetails/"+gameID+".json")
}

// OwnedGameIDs fetches the ids of all owned products. It returns them rather
// than storing them, so the caller owns the state (D2).
func (c *Client) OwnedGameIDs(ctx context.Context) ([]string, error) {
	root, err := c.getResponseJSON(ctx, c.ep.www+"/user/data/games")
	if err != nil {
		return nil, err
	}
	ids := []string{}
	owned, ok := root["owned"].([]any)
	if !ok {
		// Missing or not an array: no ids and no error.
		return ids, nil
	}
	for i, el := range owned {
		s, err := jsonval.Str(el)
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
	v, ok := root["tags"]
	if !ok || v == nil {
		return tags, nil
	}
	children, err := jsonval.Children(v)
	if err != nil {
		return nil, fmt.Errorf("webapi: tags: %w", err)
	}
	for i, child := range children {
		node, err := jsonval.Object(child)
		if err != nil {
			return nil, fmt.Errorf("webapi: tags[%d]: %w", i, err)
		}
		// A missing id/name member reads as "".
		id, err := jsonval.Str(node["id"])
		if err != nil {
			return nil, fmt.Errorf("webapi: tags[%d].id: %w", i, err)
		}
		name, err := jsonval.Str(node["name"])
		if err != nil {
			return nil, fmt.Errorf("webapi: tags[%d].name: %w", i, err)
		}
		tags[id] = name
	}
	return tags, nil
}
