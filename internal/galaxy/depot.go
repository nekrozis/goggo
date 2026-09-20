package galaxy

import (
	"context"
	"encoding/json/jsontext"
	"fmt"
	"math"
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

// maxUint64Exclusive is 2^64 — the first value past uint64. It is a float64
// constant because the fallback path of uint64Value compares a float against it.
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
func (c *Client) FilteredDepotItems(ctx context.Context, depotJSON map[string]jsontext.Value, languageRegex, arch string, opts DepotOptions) ([]model.GalaxyDepotItem, error) {
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
		name, err := memberText(raw)
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
			name, err := memberText(raw)
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

	hash, err := memberText(depotJSON["manifest"])
	if err != nil {
		return nil, fmt.Errorf("galaxy: depot manifest: %w", err)
	}
	items, err := c.DepotItems(ctx, hash, opts)
	if err != nil {
		return nil, err
	}

	productID, err := memberText(depotJSON["productId"])
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
func smallFilesContainer(depot map[string]jsontext.Value, opts DepotOptions) (*model.GalaxyDepotItem, error) {
	obj, err := memberObject(depot["smallFilesContainer"])
	if err != nil {
		return nil, fmt.Errorf("smallFilesContainer: %w", err)
	}
	if obj == nil {
		return nil, nil
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
func depotItem(raw jsontext.Value, opts DepotOptions) (*model.GalaxyDepotItem, error) {
	obj, err := memberObject(raw)
	if err != nil {
		return nil, err
	}
	if obj == nil {
		return nil, fmt.Errorf("expected a JSON object, got %s", jsonKind(raw))
	}
	chunks, isArray, err := depotChunks(obj)
	if err != nil {
		return nil, err
	}
	if !isArray {
		return nil, nil
	}

	path, err := memberText(obj["path"])
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
	if sfcRaw, ok := obj["sfcRef"]; ok && sfcRaw.Kind() != jsontext.KindNull {
		sfc, err := memberObject(sfcRaw)
		if err != nil {
			return nil, fmt.Errorf("sfcRef: %w", err)
		}
		if sfc == nil {
			return nil, fmt.Errorf("sfcRef: expected a JSON object, got %s", jsonKind(sfcRaw))
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
func depotChunks(obj map[string]jsontext.Value) (chunks []model.GalaxyDepotItemChunk, isArray bool, err error) {
	if obj["chunks"].Kind() != jsontext.KindBeginArray {
		return nil, false, nil
	}
	raw, err := memberArray(obj["chunks"])
	if err != nil {
		return nil, false, err
	}

	var compressedTotal, total uint64
	chunks = make([]model.GalaxyDepotItemChunk, 0, len(raw))
	for i, v := range raw {
		entry, err := memberObject(v)
		if err != nil {
			return nil, false, fmt.Errorf("chunks[%d]: %w", i, err)
		}
		if entry == nil {
			return nil, false, fmt.Errorf("chunks[%d]: expected a JSON object, got %s", i, jsonKind(v))
		}
		chunk := model.GalaxyDepotItemChunk{
			CompressedOffset: compressedTotal,
			Offset:           total,
		}
		if chunk.CompressedMD5, err = memberText(entry["compressedMd5"]); err != nil {
			return nil, false, fmt.Errorf("chunks[%d].compressedMd5: %w", i, err)
		}
		if chunk.MD5, err = memberText(entry["md5"]); err != nil {
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
func itemMD5(obj map[string]jsontext.Value, chunks []model.GalaxyDepotItemChunk) (string, error) {
	if raw, ok := obj["md5"]; ok {
		return memberText(raw)
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
func objectField(obj map[string]jsontext.Value, key string) (map[string]jsontext.Value, error) {
	inner, err := memberObject(obj[key])
	if err != nil {
		return nil, fmt.Errorf("%s: %w", key, err)
	}
	return inner, nil
}

// arrayField is objectField for a JSON array, with the same absent/null rule.
func arrayField(obj map[string]jsontext.Value, key string) ([]jsontext.Value, error) {
	arr, err := memberArray(obj[key])
	if err != nil {
		return nil, fmt.Errorf("%s: %w", key, err)
	}
	return arr, nil
}

// uint64Value reads one of a manifest's byte counts, sizes or offsets.
//
// The member is a JSON number, and this reader keeps it as one: the token form
// answers for a whole value inside uint64 range, and the float fallback covers
// a literal written with a fraction that is still whole ("1024.0") or an
// exponent. It is deliberately strict: a string or a boolean is a protocol
// error for a byte count, not a value to coerce. An absent or null member reads
// as 0, and a negative or out-of-range value is an error rather than a wrapped
// number.
func uint64Value(v jsontext.Value) (uint64, error) {
	switch v.Kind() {
	case jsontext.KindInvalid, jsontext.KindNull:
		return 0, nil
	case jsontext.KindNumber:
		tok, err := tokenOf(v)
		if err != nil {
			return 0, err
		}
		if n, err := tok.Uint(); err == nil {
			return n, nil
		}
		f, err := tok.Float()
		if err != nil {
			return 0, fmt.Errorf("expected a byte count, got %s", tok.String())
		}
		if math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) {
			return 0, fmt.Errorf("expected a whole byte count, got %s", tok.String())
		}
		if f < 0 || f >= maxUint64Exclusive {
			return 0, fmt.Errorf("byte count %s is outside the uint64 range", tok.String())
		}
		return uint64(f), nil
	default:
		return 0, fmt.Errorf("expected a byte count, got %s", jsonKind(v))
	}
}
