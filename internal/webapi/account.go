package webapi

import (
	"context"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"fmt"
)

// GameDetailsJSON fetches the per-game details document. A response that is not
// a JSON object is an error; the caller decides what to do with it.
func (c *Client) GameDetailsJSON(ctx context.Context, gameID string) (map[string]any, error) {
	return c.getResponseJSON(ctx, c.ep.www+"/account/gameDetails/"+gameID+".json")
}

// OwnedGameIDs fetches the ids of all owned products. It returns them rather
// than storing them, so the caller owns the state.
func (c *Client) OwnedGameIDs(ctx context.Context) ([]string, error) {
	body, err := c.getResponseBytes(ctx, c.ep.www+"/user/data/games")
	if err != nil {
		return nil, err
	}
	var root map[string]jsontext.Value
	if err := decodeObject(body, &root); err != nil {
		return nil, err
	}
	rawOwned, present := root["owned"]
	if !present || len(rawOwned) == 0 || rawOwned.Kind() != jsontext.KindBeginArray {
		// Missing or not an array: no ids and no error.
		return []string{}, nil
	}
	var items []jsontext.Value
	if err := jsonv2.Unmarshal(rawOwned, &items); err != nil {
		return nil, fmt.Errorf("webapi: owned: %w", err)
	}
	ids := make([]string, 0, len(items))
	for i, item := range items {
		s, err := readOwnedID(item, i)
		if err != nil {
			return nil, err
		}
		ids = append(ids, s)
	}
	return ids, nil
}

func readOwnedID(raw jsontext.Value, index int) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	tok, err := token(raw)
	if err != nil {
		return "", fmt.Errorf("webapi: owned[%d]: %w", index, err)
	}
	switch tok.Kind() {
	case jsontext.KindNull:
		return "", nil
	case jsontext.KindString, jsontext.KindNumber:
		return tok.String(), nil
	default:
		return "", fmt.Errorf("webapi: owned[%d]: expected a scalar, got %s", index, tok.Kind())
	}
}

type rawTagEntry struct {
	ID   jsontext.Value `json:"id"`
	Name jsontext.Value `json:"name"`
}

// Tags fetches the account tag table.
//
// A response that is not JSON is ErrNotJSON, so the CLI can render the "cookies
// have most likely expired" hint; library layers never exit. The tags value is
// iterated as array elements in order, or as object member values.
func (c *Client) Tags(ctx context.Context) (map[string]string, error) {
	body, err := c.getResponseBytes(ctx, c.ep.www+"/account/getFilteredProducts?mediaType=1&sortBy=title&system=&page=1")
	if err != nil {
		return nil, err
	}
	var root map[string]jsontext.Value
	if err := decodeObject(body, &root); err != nil {
		return nil, err
	}
	rawTags, present := root["tags"]
	if !present || len(rawTags) == 0 || rawTags.Kind() == jsontext.KindNull {
		return map[string]string{}, nil
	}
	switch rawTags.Kind() {
	case jsontext.KindBeginArray:
		return readTagsArray(rawTags)
	case jsontext.KindBeginObject:
		return readTagsObject(rawTags)
	default:
		return nil, fmt.Errorf("webapi: tags: expected a JSON array or object, got %s", rawTags.Kind())
	}
}

func readTagsArray(raw jsontext.Value) (map[string]string, error) {
	var items []jsontext.Value
	if err := jsonv2.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("webapi: tags: %w", err)
	}
	tags := make(map[string]string, len(items))
	for i, item := range items {
		if item.Kind() != jsontext.KindBeginObject {
			return nil, fmt.Errorf("webapi: tags[%d]: expected a JSON object, got %s", i, item.Kind())
		}
		var entry rawTagEntry
		if err := jsonv2.Unmarshal(item, &entry); err != nil {
			return nil, fmt.Errorf("webapi: tags[%d]: %w", i, err)
		}
		id, err := readTagScalar(entry.ID, fmt.Sprintf("[%d].id", i))
		if err != nil {
			return nil, err
		}
		name, err := readTagScalar(entry.Name, fmt.Sprintf("[%d].name", i))
		if err != nil {
			return nil, err
		}
		tags[id] = name
	}
	return tags, nil
}

func readTagsObject(raw jsontext.Value) (map[string]string, error) {
	var entries map[string]jsontext.Value
	if err := jsonv2.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("webapi: tags: %w", err)
	}
	tags := make(map[string]string, len(entries))
	for k, item := range entries {
		if item.Kind() != jsontext.KindBeginObject {
			return nil, fmt.Errorf("webapi: tags[%q]: expected a JSON object, got %s", k, item.Kind())
		}
		var entry rawTagEntry
		if err := jsonv2.Unmarshal(item, &entry); err != nil {
			return nil, fmt.Errorf("webapi: tags[%q]: %w", k, err)
		}
		id, err := readTagScalar(entry.ID, fmt.Sprintf("[%q].id", k))
		if err != nil {
			return nil, err
		}
		// If entry has no explicit or non-empty id, use the dictionary key.
		if id == "" {
			id = k
		}
		name, err := readTagScalar(entry.Name, fmt.Sprintf("[%q].name", k))
		if err != nil {
			return nil, err
		}
		tags[id] = name
	}
	return tags, nil
}

func readTagScalar(raw jsontext.Value, contextPath string) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	tok, err := token(raw)
	if err != nil {
		return "", fmt.Errorf("webapi: tags%s: %w", contextPath, err)
	}
	switch tok.Kind() {
	case jsontext.KindNull:
		return "", nil
	case jsontext.KindString, jsontext.KindNumber:
		return tok.String(), nil
	default:
		return "", fmt.Errorf("webapi: tags%s: expected a scalar, got %s", contextPath, tok.Kind())
	}
}
