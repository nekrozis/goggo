package core

// depotCandidate is one base-content depot of a build: the entry the language
// resolution, the architecture listing and the install-options table all read.
type depotCandidate struct {
	manifestHash string
	languages    []string
	osBitness    []string
	index        int
	size         int64
}

// depotCandidates collects a build's base-content depots.
//
// It is the single definition of that set. The resolution of a --language
// request, the install-options table and the plan's content guard all work from
// it, so a language one of them lists can never be one another rejects.
//
// GOG support metadata depots are not content, DLC depots are not base content,
// and a depot without a manifest has nothing to expand: all three drop out.
func depotCandidates(manifest map[string]any, baseProductID string) ([]depotCandidate, error) {
	rawDepots, err := manifestArray(manifest, "depots")
	if err != nil {
		return nil, err
	}

	var candidates []depotCandidate
	for i, raw := range rawDepots {
		depot, err := mapObject(raw)
		if err != nil {
			continue
		}
		if isGogDepot(depot) || !isBaseDepot(depot, baseProductID) {
			continue
		}
		manifestHash, _ := scalarString(depot["manifest"])
		if manifestHash == "" {
			continue
		}

		var langs []string
		rawLangs, _ := manifestArray(depot, "languages")
		for _, rl := range rawLangs {
			if s, err := scalarString(rl); err == nil && s != "" {
				langs = append(langs, s)
			}
		}

		var bitness []string
		rawBitness, _ := manifestArray(depot, "osBitness")
		for _, rb := range rawBitness {
			if s, err := scalarString(rb); err == nil && s != "" {
				bitness = append(bitness, s)
			}
		}

		var depotSize int64
		if sz, err := intValue(depot["size"]); err == nil && sz > 0 {
			depotSize = sz
		}

		candidates = append(candidates, depotCandidate{
			index:        i,
			manifestHash: manifestHash,
			languages:    langs,
			osBitness:    bitness,
			size:         depotSize,
		})
	}
	return candidates, nil
}
