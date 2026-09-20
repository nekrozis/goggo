package webapi

import (
	"context"
	"encoding/json/jsontext"
	"fmt"

	"github.com/nekrozis/goggo/internal/jsonread"
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
	owned, err := jsonread.Array(raw)
	if err != nil {
		return nil, fmt.Errorf("webapi: owned: %w", err)
	}
	for i, el := range owned {
		// An owned id is an identifier: the account's own list mixes a string
		// entry with a numeric one, and both spell the same product id.
		s, err := jsonread.Scalar(el)
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
		node, err := jsonread.Object(child)
		if err != nil {
			return nil, fmt.Errorf("webapi: tags[%d]: %w", i, err)
		}
		// A missing id/name member reads as "".
		//
		// The id is the identifier family — the live API sends it as a number
		// while this account's fixture sends a string — and the name is text.
		id, err := jsonread.Scalar(node["id"])
		if err != nil {
			return nil, fmt.Errorf("webapi: tags[%d].id: %w", i, err)
		}
		name, err := jsonread.Text(node["name"])
		if err != nil {
			return nil, fmt.Errorf("webapi: tags[%d].name: %w", i, err)
		}
		tags[id] = name
	}
	return tags, nil
}

// containerValues returns the values of a container: array elements in their
// document order, or the member values of an object. Both shapes are accepted
// because the account's tag table is sent either way, and the caller wants the
// values rather than the container kind.
func containerValues(v jsontext.Value) ([]jsontext.Value, error) {
	switch v.Kind() {
	case jsontext.KindBeginArray:
		return jsonread.Array(v)
	case jsontext.KindBeginObject:
		obj, err := jsonread.Object(v)
		if err != nil {
			return nil, err
		}
		out := make([]jsontext.Value, 0, len(obj))
		for _, e := range obj {
			out = append(out, e)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("expected a JSON array or object, got %s", jsonread.Kind(v))
	}
}
