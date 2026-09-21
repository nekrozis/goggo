package galaxy

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/model"
	"github.com/nekrozis/goggo/internal/util"
)

// smallFilesContainerName is the path the synthetic small-files container entry
// gets. It stays private: the consumer recognises the entry through
// GalaxyDepotItem.IsSmallFilesContainer, so nothing outside needs the literal.
const smallFilesContainerName = "galaxy_smallfilescontainer"

// DepotOptions carries the download configuration needed while expanding a
// manifest: the dependency switch, the lowercase-path rule and the platform that
// rule applies to.
//
// It is a parameter rather than a Client field so that this package keeps
// knowing nothing about the download configuration.
type DepotOptions struct {
	IsDependency   bool
	LowercasePaths bool
	Platform       uint32
}

type rawDepot struct {
	SmallFilesContainer jsontext.Value `json:"smallFilesContainer"`
	Items               jsontext.Value `json:"items"`
}

type rawSFC struct {
	MD5    jsontext.Value `json:"md5"`
	Chunks jsontext.Value `json:"chunks"`
}

type rawDepotItem struct {
	Path   jsontext.Value `json:"path"`
	MD5    jsontext.Value `json:"md5"`
	Chunks jsontext.Value `json:"chunks"`
	SFCRef jsontext.Value `json:"sfcRef"`
}

type rawSFCRef struct {
	Offset jsontext.Value `json:"offset"`
	Size   jsontext.Value `json:"size"`
}

type rawChunk struct {
	CompressedMD5  string         `json:"compressedMd5"`
	MD5            string         `json:"md5"`
	CompressedSize jsontext.Value `json:"compressedSize"`
	Size           jsontext.Value `json:"size"`
}

func (c *Client) manifestURL(manifestHash string, isDependency bool) string {
	hash := manifestHash
	if hash != "" {
		hash = HashToGalaxyPath(hash)
	}
	if isDependency {
		return c.ep.cdn + "/content-system/v2/dependencies/meta/" + hash
	}
	return c.ep.cdn + "/content-system/v2/meta/" + hash
}

// DepotItems expands a manifest into depot entries.
//
// The order is the small-files container first when the manifest carries one,
// then the entries of depot.items in document order. An entry whose "chunks" is
// not an array is skipped — an explicit filter, not an error, and a file can
// legitimately have no chunks.
//
// An empty hash reaches ManifestV2 unchanged and produces the request that step
// already defines.
func (c *Client) DepotItems(ctx context.Context, hash string, opts DepotOptions) ([]model.GalaxyDepotItem, error) {
	target := c.manifestURL(hash, opts.IsDependency)
	raw, err := c.getResponseBytes(ctx, target)
	if err != nil {
		return nil, err
	}
	if plain, ok := inflateZlibBytes(raw); ok {
		raw = plain
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("%w: empty body", ErrNotJSON)
	}

	var manifest map[string]jsontext.Value
	if err := jsonv2.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotJSON, err)
	}

	depotVal, ok := manifest["depot"]
	if !ok || len(depotVal) == 0 || depotVal.Kind() == jsontext.KindNull {
		return nil, nil
	}
	if depotVal.Kind() != jsontext.KindBeginObject {
		return nil, fmt.Errorf("galaxy: manifest depot: expected a JSON object, got %s", kindName(depotVal.Kind()))
	}

	var depot rawDepot
	if err := jsonv2.Unmarshal(depotVal, &depot); err != nil {
		return nil, fmt.Errorf("galaxy: manifest depot: %w", err)
	}

	var items []model.GalaxyDepotItem
	container, err := smallFilesContainer(depot.SmallFilesContainer, opts)
	if err != nil {
		return nil, fmt.Errorf("galaxy: manifest depot: %w", err)
	}
	if container != nil {
		items = append(items, *container)
	}

	if len(depot.Items) > 0 && depot.Items.Kind() != jsontext.KindNull {
		if depot.Items.Kind() != jsontext.KindBeginArray {
			return nil, fmt.Errorf("galaxy: manifest depot: items: expected a JSON array, got %s", kindName(depot.Items.Kind()))
		}
		var rawEntries []jsontext.Value
		if err := jsonv2.Unmarshal(depot.Items, &rawEntries); err != nil {
			return nil, fmt.Errorf("galaxy: manifest depot: %w", err)
		}
		for i, rawItem := range rawEntries {
			item, err := depotItem(rawItem, opts)
			if err != nil {
				return nil, fmt.Errorf("galaxy: manifest depot items[%d]: %w", i, err)
			}
			if item != nil {
				items = append(items, *item)
			}
		}
	}
	return items, nil
}

// FilteredDepotItems expands the depot entry of a manifest into items, keeping
// only the entries the language and the architecture select.
//
// The language test: an entry matches when the depot lists "*" or a language the
// anchored, case-insensitive regex finds, so an empty or missing "languages"
// list selects nothing. The architecture test: a missing or null "osBitness"
// means the entry is not architecture-specific and is selected, otherwise the
// list must contain "*" or the requested arch.
//
// languageRegex and arch are chosen by the caller, from config.Languages[].Regexp
// and config.GalaxyArchs[].Code.
func (c *Client) FilteredDepotItems(ctx context.Context, depotJSON map[string]any, languageRegex, arch string, opts DepotOptions) ([]model.GalaxyDepotItem, error) {
	languageRE, err := regexp.Compile("(?i)^(" + languageRegex + ")$")
	if err != nil {
		// Compiled before anything is fetched, so a bad pattern costs no request.
		return nil, fmt.Errorf("galaxy: depot language regexp %q: %w", languageRegex, err)
	}

	languages, err := arrayField(depotJSON, "languages")
	if err != nil {
		return nil, fmt.Errorf("galaxy: depot languages: %w", err)
	}
	selectedLanguage := false
	for i, raw := range languages {
		name, err := mapString(raw)
		if err != nil {
			return nil, fmt.Errorf("galaxy: depot languages[%d]: %w", i, err)
		}
		if name == "*" || languageRE.MatchString(name) {
			selectedLanguage = true
			break
		}
	}

	selectedArch := true
	bitness, err := arrayField(depotJSON, "osBitness")
	if err != nil {
		return nil, fmt.Errorf("galaxy: depot osBitness: %w", err)
	}
	if bitness != nil {
		selectedArch = false
		for i, raw := range bitness {
			name, err := mapString(raw)
			if err != nil {
				return nil, fmt.Errorf("galaxy: depot osBitness[%d]: %w", i, err)
			}
			if name == "*" || name == arch {
				selectedArch = true
				break
			}
		}
	}

	if !selectedLanguage || !selectedArch {
		return nil, nil
	}

	hash, err := mapString(depotJSON["manifest"])
	if err != nil {
		return nil, fmt.Errorf("galaxy: depot manifest: %w", err)
	}
	items, err := c.DepotItems(ctx, hash, opts)
	if err != nil {
		return nil, err
	}

	productID, err := mapString(depotJSON["productId"])
	if err != nil {
		return nil, fmt.Errorf("galaxy: depot productId: %w", err)
	}
	if productID != "" {
		for i := range items {
			items[i].ProductID = productID
		}
	}
	return items, nil
}

// smallFilesContainer decodes the synthetic entry built from
// depot.smallFilesContainer, or returns nil when the
// manifest carries no usable container.
//
// Its path is the container constant and is NOT normalised: the lowercase and
// separator handling applies to depot.items only.
func smallFilesContainer(raw jsontext.Value, opts DepotOptions) (*model.GalaxyDepotItem, error) {
	if len(raw) == 0 || raw.Kind() == jsontext.KindNull {
		return nil, nil
	}
	if raw.Kind() != jsontext.KindBeginObject {
		return nil, fmt.Errorf("smallFilesContainer: expected a JSON object, got %s", kindName(raw.Kind()))
	}
	var obj rawSFC
	if err := jsonv2.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("smallFilesContainer: %w", err)
	}
	chunks, isArray, err := depotChunks(obj.Chunks)
	if err != nil {
		return nil, fmt.Errorf("smallFilesContainer: %w", err)
	}
	if !isArray {
		return nil, nil
	}
	md5, err := itemMD5(obj.MD5, chunks)
	if err != nil {
		return nil, fmt.Errorf("smallFilesContainer: %w", err)
	}
	item := newDepotItem(chunks, smallFilesContainerName, md5, opts)
	item.IsSmallFilesContainer = true
	return item, nil
}

// depotItem decodes one entry of depot.items. A nil item means "skipped": the
// entry has no chunks array.
func depotItem(raw jsontext.Value, opts DepotOptions) (*model.GalaxyDepotItem, error) {
	if raw.Kind() != jsontext.KindBeginObject {
		return nil, fmt.Errorf("expected a JSON object, got %s", kindName(raw.Kind()))
	}
	var obj rawDepotItem
	if err := jsonv2.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}

	chunks, isArray, err := depotChunks(obj.Chunks)
	if err != nil {
		return nil, err
	}
	if !isArray {
		return nil, nil
	}

	var path string
	if len(obj.Path) > 0 && obj.Path.Kind() != jsontext.KindNull {
		tok, err := token(obj.Path)
		if err != nil {
			return nil, fmt.Errorf("path: %w", err)
		}
		if tok.Kind() != jsontext.KindString {
			return nil, fmt.Errorf("path: expected a string, got %s", kindName(tok.Kind()))
		}
		path = tok.String()
	}

	// Order matters: lowercase first, then the separator rewrite.
	if opts.LowercasePaths && opts.Platform == config.PlatformWindows {
		path = strings.ToLower(path)
	}
	path, _ = util.ReplaceAll(path, "\\", "/")

	md5, err := itemMD5(obj.MD5, chunks)
	if err != nil {
		return nil, err
	}
	item := newDepotItem(chunks, path, md5, opts)

	// "sfcRef" present and non-null marks a file stored inside the small-files
	// container. A null member is treated as absent: it carries no range.
	if len(obj.SFCRef) > 0 && obj.SFCRef.Kind() != jsontext.KindNull {
		if obj.SFCRef.Kind() != jsontext.KindBeginObject {
			return nil, fmt.Errorf("sfcRef: expected a JSON object, got %s", kindName(obj.SFCRef.Kind()))
		}
		var sfc rawSFCRef
		if err := jsonv2.Unmarshal(obj.SFCRef, &sfc); err != nil {
			return nil, fmt.Errorf("sfcRef: %w", err)
		}
		item.IsInSFC = true
		if item.SFCOffset, err = readDepotSize(sfc.Offset, "sfcRef.offset"); err != nil {
			return nil, err
		}
		if item.SFCSize, err = readDepotSize(sfc.Size, "sfcRef.size"); err != nil {
			return nil, err
		}
	}
	return item, nil
}

// newDepotItem assembles an entry and totals its chunks.
func newDepotItem(chunks []model.GalaxyDepotItemChunk, path, md5 string, opts DepotOptions) *model.GalaxyDepotItem {
	item := &model.GalaxyDepotItem{
		Chunks:       chunks,
		Path:         path,
		MD5:          md5,
		IsDependency: opts.IsDependency,
	}
	for _, c := range chunks {
		item.TotalCompressedSize += c.CompressedSize
		item.TotalSize += c.Size
	}
	return item
}

// depotChunks decodes an entry's "chunks" array and assigns the running byte
// ranges. The offset of a chunk is where the previous ones ended, so it is
// recorded before the sizes are added.
//
// The second return value is false when "chunks" is missing, null or not an
// array; both callers treat that as "skip this entry".
func depotChunks(chunksVal jsontext.Value) (chunks []model.GalaxyDepotItemChunk, isArray bool, err error) {
	if len(chunksVal) == 0 || chunksVal.Kind() == jsontext.KindNull || chunksVal.Kind() != jsontext.KindBeginArray {
		return nil, false, nil
	}

	var rawChunks []rawChunk
	if err := jsonv2.Unmarshal(chunksVal, &rawChunks); err != nil {
		return nil, false, err
	}

	var compressedTotal, total uint64
	chunks = make([]model.GalaxyDepotItemChunk, len(rawChunks))
	for i, c := range rawChunks {
		compressedSize, err := readDepotSize(c.CompressedSize, fmt.Sprintf("chunks[%d].compressedSize", i))
		if err != nil {
			return nil, false, err
		}
		size, err := readDepotSize(c.Size, fmt.Sprintf("chunks[%d].size", i))
		if err != nil {
			return nil, false, err
		}
		chunks[i] = model.GalaxyDepotItemChunk{
			CompressedMD5:    c.CompressedMD5,
			MD5:              c.MD5,
			CompressedSize:   compressedSize,
			Size:             size,
			CompressedOffset: compressedTotal,
			Offset:           total,
		}
		compressedTotal += compressedSize
		total += size
	}
	return chunks, true, nil
}

// itemMD5 is the three-way fallback for an entry's hash: its own "md5" when the
// member is present (including null or empty string), else the md5 of the single
// chunk when there is exactly one, else "".
func itemMD5(md5Val jsontext.Value, chunks []model.GalaxyDepotItemChunk) (string, error) {
	if len(md5Val) > 0 {
		if md5Val.Kind() == jsontext.KindNull {
			return "", nil
		}
		tok, err := token(md5Val)
		if err != nil {
			return "", err
		}
		if tok.Kind() != jsontext.KindString {
			return "", fmt.Errorf("expected a string, got %s", kindName(tok.Kind()))
		}
		return tok.String(), nil
	}
	if len(chunks) == 1 {
		return chunks[0].MD5, nil
	}
	return "", nil
}

// arrayField is objectField for a JSON array, with the same absent/null rule.
func arrayField(obj map[string]any, key string) ([]any, error) {
	v, ok := obj[key]
	if !ok || v == nil {
		return nil, nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("%s: expected a JSON array, got %s", key, mapKind(v))
	}
	return arr, nil
}

// readDepotSize reads a manifest's byte count, size or offset.
//
// It rejects negative numbers, fractions, strings, booleans and values that
// overflow uint64. An absent or null member reads as 0.
func readDepotSize(v jsontext.Value, field string) (uint64, error) {
	if len(v) == 0 || v.Kind() == jsontext.KindNull {
		return 0, nil
	}
	if v.Kind() != jsontext.KindNumber {
		return 0, fmt.Errorf("%s: expected a byte count, got %s", field, kindName(v.Kind()))
	}
	tok, err := token(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", field, err)
	}
	val, err := tok.Uint()
	if err != nil {
		return 0, fmt.Errorf("%s: %w", field, err)
	}
	return val, nil
}

func token(v jsontext.Value) (jsontext.Token, error) {
	if len(v) == 0 {
		return jsontext.Token{}, io.ErrUnexpectedEOF
	}
	dec := jsontext.NewDecoder(bytes.NewReader(v))
	return dec.ReadToken()
}

func kindName(k jsontext.Kind) string {
	switch k {
	case jsontext.KindNull:
		return "null"
	case jsontext.KindTrue, jsontext.KindFalse:
		return "boolean"
	case jsontext.KindString:
		return "string"
	case jsontext.KindNumber:
		return "number"
	case jsontext.KindBeginArray:
		return "array"
	case jsontext.KindBeginObject:
		return "object"
	default:
		return k.String()
	}
}

func mapKind(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case float64, int, int64, uint64:
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return fmt.Sprintf("%T", v)
	}
}

func mapString(v any) (string, error) {
	if v == nil {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("expected a string, got %s", mapKind(v))
	}
	return s, nil
}
