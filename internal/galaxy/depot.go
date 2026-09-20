package galaxy

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strings"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/jsonval"
	"github.com/nekrozis/goggo/internal/model"
	"github.com/nekrozis/goggo/internal/util"
)

// smallFilesContainerName is the path the synthetic small-files container entry
// gets. It stays private: the consumer recognises the entry through
// GalaxyDepotItem.IsSmallFilesContainer, so nothing outside needs the literal.
const smallFilesContainerName = "galaxy_smallfilescontainer"

// maxUint64Exclusive is 2^64 — the first value past uint64. It is a float64
// constant because the values compared against it come from encoding/json.
const maxUint64Exclusive = float64(1 << 64)

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
	manifest, err := c.ManifestV2(ctx, hash, opts.IsDependency)
	if err != nil {
		return nil, err
	}

	depot, err := objectField(manifest, "depot")
	if err != nil {
		return nil, fmt.Errorf("galaxy: manifest depot: %w", err)
	}
	if depot == nil {
		return nil, nil
	}

	var items []model.GalaxyDepotItem
	container, err := smallFilesContainer(depot, opts)
	if err != nil {
		return nil, fmt.Errorf("galaxy: manifest depot: %w", err)
	}
	if container != nil {
		items = append(items, *container)
	}

	entries, err := arrayField(depot, "items")
	if err != nil {
		return nil, fmt.Errorf("galaxy: manifest depot: %w", err)
	}
	for i, raw := range entries {
		item, err := depotItem(raw, opts)
		if err != nil {
			return nil, fmt.Errorf("galaxy: manifest depot items[%d]: %w", i, err)
		}
		if item != nil {
			items = append(items, *item)
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
		name, err := jsonval.Str(raw)
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
			name, err := jsonval.Str(raw)
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

	hash, err := jsonval.Str(depotJSON["manifest"])
	if err != nil {
		return nil, fmt.Errorf("galaxy: depot manifest: %w", err)
	}
	items, err := c.DepotItems(ctx, hash, opts)
	if err != nil {
		return nil, err
	}

	productID, err := jsonval.Str(depotJSON["productId"])
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
// depot.smallFilesContainer , or returns nil when the
// manifest carries no usable container.
//
// Its path is the container constant and is NOT normalised: the lowercase and
// separator handling applies to depot.items only.
func smallFilesContainer(depot map[string]any, opts DepotOptions) (*model.GalaxyDepotItem, error) {
	raw, ok := depot["smallFilesContainer"]
	if !ok || raw == nil {
		return nil, nil
	}
	obj, err := jsonval.Object(raw)
	if err != nil {
		return nil, fmt.Errorf("smallFilesContainer: %w", err)
	}
	chunks, isArray, err := depotChunks(obj)
	if err != nil {
		return nil, fmt.Errorf("smallFilesContainer: %w", err)
	}
	if !isArray {
		return nil, nil
	}
	md5, err := itemMD5(obj, chunks)
	if err != nil {
		return nil, fmt.Errorf("smallFilesContainer: %w", err)
	}
	item := newDepotItem(chunks, smallFilesContainerName, md5, opts)
	item.IsSmallFilesContainer = true
	return item, nil
}

// depotItem decodes one entry of depot.items. A nil item means "skipped": the
// entry has no chunks array.
func depotItem(raw any, opts DepotOptions) (*model.GalaxyDepotItem, error) {
	obj, err := jsonval.Object(raw)
	if err != nil {
		return nil, err
	}
	chunks, isArray, err := depotChunks(obj)
	if err != nil {
		return nil, err
	}
	if !isArray {
		return nil, nil
	}

	path, err := jsonval.Str(obj["path"])
	if err != nil {
		return nil, fmt.Errorf("path: %w", err)
	}
	// Order matters: lowercase first, then the separator rewrite.
	if opts.LowercasePaths && opts.Platform == config.PlatformWindows {
		path = strings.ToLower(path)
	}
	path, _ = util.ReplaceAll(path, "\\", "/")

	md5, err := itemMD5(obj, chunks)
	if err != nil {
		return nil, err
	}
	item := newDepotItem(chunks, path, md5, opts)

	// "sfcRef" present and non-null marks a file stored inside the small-files
	// container. A null member is treated as absent: it carries no range.
	if raw, ok := obj["sfcRef"]; ok && raw != nil {
		sfc, err := jsonval.Object(raw)
		if err != nil {
			return nil, fmt.Errorf("sfcRef: %w", err)
		}
		item.IsInSFC = true
		if item.SFCOffset, err = uint64Value(sfc["offset"]); err != nil {
			return nil, fmt.Errorf("sfcRef.offset: %w", err)
		}
		if item.SFCSize, err = uint64Value(sfc["size"]); err != nil {
			return nil, fmt.Errorf("sfcRef.size: %w", err)
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
func depotChunks(obj map[string]any) (chunks []model.GalaxyDepotItemChunk, isArray bool, err error) {
	raw, ok := obj["chunks"].([]any)
	if !ok {
		return nil, false, nil
	}

	var compressedTotal, total uint64
	chunks = make([]model.GalaxyDepotItemChunk, 0, len(raw))
	for i, v := range raw {
		entry, err := jsonval.Object(v)
		if err != nil {
			return nil, false, fmt.Errorf("chunks[%d]: %w", i, err)
		}
		chunk := model.GalaxyDepotItemChunk{
			CompressedOffset: compressedTotal,
			Offset:           total,
		}
		if chunk.CompressedMD5, err = jsonval.Str(entry["compressedMd5"]); err != nil {
			return nil, false, fmt.Errorf("chunks[%d].compressedMd5: %w", i, err)
		}
		if chunk.MD5, err = jsonval.Str(entry["md5"]); err != nil {
			return nil, false, fmt.Errorf("chunks[%d].md5: %w", i, err)
		}
		if chunk.CompressedSize, err = uint64Value(entry["compressedSize"]); err != nil {
			return nil, false, fmt.Errorf("chunks[%d].compressedSize: %w", i, err)
		}
		if chunk.Size, err = uint64Value(entry["size"]); err != nil {
			return nil, false, fmt.Errorf("chunks[%d].size: %w", i, err)
		}
		compressedTotal += chunk.CompressedSize
		total += chunk.Size
		chunks = append(chunks, chunk)
	}
	return chunks, true, nil
}

// itemMD5 is the three-way fallback for an entry's hash: its own "md5" when the
// member is present, else the md5 of the single chunk when there is exactly one,
// else "".
func itemMD5(obj map[string]any, chunks []model.GalaxyDepotItemChunk) (string, error) {
	if _, ok := obj["md5"]; ok {
		return jsonval.Str(obj["md5"])
	}
	if len(chunks) == 1 {
		return chunks[0].MD5, nil
	}
	return "", nil
}

// objectField reads a member that has to be a JSON object: absent or null is
// (nil, nil) — "not present" — while any other type is an error, because a
// document that carries the section in the wrong shape is broken rather than
// empty.
func objectField(obj map[string]any, key string) (map[string]any, error) {
	v, ok := obj[key]
	if !ok || v == nil {
		return nil, nil
	}
	inner, err := jsonval.Object(v)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", key, err)
	}
	return inner, nil
}

// arrayField is objectField for a JSON array, with the same absent/null rule.
func arrayField(obj map[string]any, key string) ([]any, error) {
	v, ok := obj[key]
	if !ok || v == nil {
		return nil, nil
	}
	arr, err := jsonval.Array(v)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", key, err)
	}
	return arr, nil
}

// uint64Value reads one of a manifest's byte counts, sizes or offsets.
//
// A JSON number arrives as a float64, so precision above 2^53 is already lost;
// the helper accepts non-negative whole values in the uint64 range. It is
// deliberately strict: a string or a boolean is a protocol error for a byte
// count, not a value to coerce. An absent or null member reads as 0.
func uint64Value(v any) (uint64, error) {
	switch t := v.(type) {
	case nil:
		return 0, nil
	case int:
		if t < 0 {
			return 0, fmt.Errorf("negative byte count %d", t)
		}
		return uint64(t), nil
	case int64:
		if t < 0 {
			return 0, fmt.Errorf("negative byte count %d", t)
		}
		return uint64(t), nil
	case uint64:
		return t, nil
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) || t != math.Trunc(t) {
			return 0, fmt.Errorf("expected a whole byte count, got %v", t)
		}
		if t < 0 || t >= maxUint64Exclusive {
			return 0, fmt.Errorf("byte count %v is outside the uint64 range", t)
		}
		return uint64(t), nil
	default:
		return 0, fmt.Errorf("expected a byte count, got %s", jsonval.Kind(v))
	}
}
