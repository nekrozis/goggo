package gamedetails

// GetDetailsAsJson returns the object the `list json` output is built from. The
// field set, the vector order (extras, installers, patches, languagepacks — the
// display order, deliberately not the file-vector order) and the DLC recursion
// are all part of the output contract. Empty vectors are absent members: a key
// appears exactly when it has entries.
func (gd *GameDetails) GetDetailsAsJson() map[string]any {
	out := map[string]any{
		"gamename":          gd.Gamename,
		"gamename_basegame": gd.GamenameBasegame,
		"product_id":        gd.ProductID,
		"title":             gd.Title,
		"title_basegame":    gd.TitleBasegame,
		"icon":              gd.Icon,
		"serials":           gd.Serials,
		"changelog":         gd.Changelog,
	}
	for _, v := range []struct {
		key  string
		list []GameFile
	}{
		{"extras", gd.Extras},
		{"installers", gd.Installers},
		{"patches", gd.Patches},
		{"languagepacks", gd.LanguagePacks},
	} {
		if len(v.list) == 0 {
			continue
		}
		files := make([]any, 0, len(v.list))
		for i := range v.list {
			files = append(files, v.list[i].GetAsJson())
		}
		out[v.key] = files
	}
	if gd.Downlink != nil {
		out["downlink_diag"] = map[string]any{
			"attempts":     gd.Downlink.Attempts,
			"failures":     gd.Downlink.Failures,
			"usable":       gd.Downlink.Usable,
			"first_error":  gd.Downlink.FirstError,
			"full_failure": gd.Downlink.FullFailure(),
		}
	}
	if len(gd.DLCs) > 0 {
		dlcs := make([]any, 0, len(gd.DLCs))
		for i := range gd.DLCs {
			dlcs = append(dlcs, gd.DLCs[i].GetDetailsAsJson())
		}
		out["dlcs"] = dlcs
	}
	return out
}
