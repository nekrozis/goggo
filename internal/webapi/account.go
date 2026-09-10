package webapi

import (
	"context"
	"fmt"
)

// GameDetailsJSON fetches the per-game details document (website.cpp:83-89).
// The C++ version returns whatever Json::Value came back and lets the caller
// treat an empty value as "no details"; here a response that is not a JSON
// object is an error and the caller decides what to do with it.
func (c *Client) GameDetailsJSON(ctx context.Context, gameID string) (map[string]any, error) {
	return c.getResponseJSON(ctx, c.ep.www+"/account/gameDetails/"+gameID+".json")
}

// OwnedGameIDs fetches the ids of all owned products (website.cpp:846-858).
// The C++ source stores them in the process-wide Globals::vOwnedGamesIds; the
// Go port returns them so the caller owns the state (review lock, D2).
func (c *Client) OwnedGameIDs(ctx context.Context) ([]string, error) {
	root, err := c.getResponseJSON(ctx, c.ep.www+"/user/data/games")
	if err != nil {
		return nil, err
	}
	ids := []string{}
	owned, ok := root["owned"].([]any)
	if !ok {
		// Missing or not an array: jsoncpp iterates a scalar value as an empty
		// range, so the original yields no ids and no error either.
		return ids, nil
	}
	for i, el := range owned {
		s, err := jsonStrLoose(el)
		if err != nil {
			return nil, fmt.Errorf("webapi: owned[%d]: %w", i, err)
		}
		ids = append(ids, s)
	}
	return ids, nil
}

// Tags fetches the account tag table (website.cpp:799-844).
//
// The C++ source exits the process when the response is not JSON, printing a
// hint that the cookies have most likely expired; here that case is
// ErrNotJSON so the CLI can render the hint (review lock: library layers never
// exit). The tags value is iterated the way jsoncpp ranges over it — array
// elements in order, or object member values — because the original does not
// assert the container kind.
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
	children, err := jsonChildren(v)
	if err != nil {
		return nil, fmt.Errorf("webapi: tags: %w", err)
	}
	for i, child := range children {
		node, err := jsonObject(child)
		if err != nil {
			return nil, fmt.Errorf("webapi: tags[%d]: %w", i, err)
		}
		// A missing id/name member reads as null in jsoncpp and stringifies to
		// "", so both are read loosely.
		id, err := jsonStrLoose(node["id"])
		if err != nil {
			return nil, fmt.Errorf("webapi: tags[%d].id: %w", i, err)
		}
		name, err := jsonStrLoose(node["name"])
		if err != nil {
			return nil, fmt.Errorf("webapi: tags[%d].name: %w", i, err)
		}
		tags[id] = name
	}
	return tags, nil
}
