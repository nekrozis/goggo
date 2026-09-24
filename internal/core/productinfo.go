package core

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"strings"

	"github.com/nekrozis/goggo/internal/model"
)

// ResolveProductRef resolves a product reference (numeric ID or slug) to a numeric product ID.
// A numeric ID is returned directly without requiring an active account session.
// A slug requires an authenticated session; if called while not logged in, it returns
// a clear login requirement error.
func (d *Downloader) ResolveProductRef(ctx context.Context, ref string, mode ProductRefMode) (string, error) {
	if numericIDRE.MatchString(ref) {
		return ref, nil
	}
	if !d.LoggedIn() {
		return "", fmt.Errorf("cannot resolve game %q: login required (log in with 'goggo auth login' or specify numeric product ID)", ref)
	}
	id, notice, err := d.selectProductID(ctx, ref, mode)
	if err != nil {
		return "", err
	}
	if notice.Err {
		return "", errors.New(notice.Text)
	}
	return id, nil
}

// ProductInfo retrieves the canonical ProductInfo for a numeric product ID.
func (d *Downloader) ProductInfo(ctx context.Context, productID string) (model.ProductInfo, error) {
	doc, err := d.galaxy.Product(ctx, productID)
	if err != nil {
		return model.ProductInfo{}, err
	}
	return parseProductInfo(doc.JSON)
}

// GetProductInfo resolves a product reference and fetches its ProductInfo.
func (d *Downloader) GetProductInfo(ctx context.Context, ref string, mode ProductRefMode) (model.ProductInfo, error) {
	productID, err := d.ResolveProductRef(ctx, ref, mode)
	if err != nil {
		return model.ProductInfo{}, err
	}
	return d.ProductInfo(ctx, productID)
}

type rawProductInfoDoc struct {
	Images                     rawProductImages `json:"images"`
	Title                      string           `json:"title"`
	ReleaseDate                string           `json:"release_date"`
	ID                         jsontext.Value   `json:"id"`
	Slug                       jsontext.Value   `json:"slug"`
	Description                jsontext.Value   `json:"description"`
	Tags                       jsontext.Value   `json:"tags"`
	Genres                     jsontext.Value   `json:"genres"`
	ExpandedDLCs               []jsontext.Value `json:"expanded_dlcs"`
	ContentSystemCompatibility rawCompatibility `json:"content_system_compatibility"`
	Platforms                  rawCompatibility `json:"platforms"`
}

type rawCompatibility struct {
	Windows bool `json:"windows"`
	OSX     bool `json:"osx"`
	Mac     bool `json:"mac"`
	Linux   bool `json:"linux"`
}

type rawProductImages struct {
	Icon string `json:"icon"`
	Logo string `json:"logo"`
}

type rawDLCItem struct {
	Title string         `json:"title"`
	ID    jsontext.Value `json:"id"`
	Slug  jsontext.Value `json:"slug"`
}

func parseProductInfo(raw []byte) (model.ProductInfo, error) {
	if len(raw) == 0 {
		return model.ProductInfo{}, errors.New("productinfo: empty JSON body")
	}

	var rawDoc rawProductInfoDoc
	if err := jsonv2.Unmarshal(raw, &rawDoc); err != nil {
		return model.ProductInfo{}, fmt.Errorf("productinfo: unmarshal: %w", err)
	}

	id, err := readStringOrNumber(rawDoc.ID)
	if err != nil {
		return model.ProductInfo{}, fmt.Errorf("productinfo id: %w", err)
	}

	slug, err := readStringValue(rawDoc.Slug)
	if err != nil {
		return model.ProductInfo{}, fmt.Errorf("productinfo slug: %w", err)
	}

	info := model.ProductInfo{
		ID:          id,
		Slug:        slug,
		Title:       rawDoc.Title,
		ReleaseDate: cleanReleaseDate(rawDoc.ReleaseDate),
	}

	// Platforms: check content_system_compatibility first, fallback to platforms.
	comp := rawDoc.ContentSystemCompatibility
	if !comp.Windows && !comp.OSX && !comp.Mac && !comp.Linux {
		comp = rawDoc.Platforms
	}
	if comp.Windows {
		info.Platforms = append(info.Platforms, "Windows")
	}
	if comp.OSX || comp.Mac {
		info.Platforms = append(info.Platforms, "Mac")
	}
	if comp.Linux {
		info.Platforms = append(info.Platforms, "Linux")
	}

	// Images: normalize scheme-relative URL.
	if rawDoc.Images.Icon != "" {
		info.Icon = normalizeImageURL(rawDoc.Images.Icon)
	}
	if rawDoc.Images.Logo != "" {
		logo := normalizeImageURL(rawDoc.Images.Logo)
		logo = strings.Replace(logo, "_glx_logo.jpg", ".jpg", 1)
		info.Logo = logo
	}

	// Description: can be string or object with lead/full.
	info.Description = parseDescription(rawDoc.Description)

	// Tags and genres are optional fields retained for schema tolerance;
	// the current Galaxy Product API endpoint does not reliably populate them.
	info.Tags = parseNameList(rawDoc.Tags)
	info.Genres = parseNameList(rawDoc.Genres)

	// Expanded DLCs.
	for _, rawDLC := range rawDoc.ExpandedDLCs {
		if len(rawDLC) == 0 || rawDLC.Kind() != jsontext.KindBeginObject {
			continue
		}
		var item rawDLCItem
		if err := jsonv2.Unmarshal(rawDLC, &item); err == nil {
			dlcID, _ := readStringOrNumber(item.ID)
			dlcSlug, _ := readStringValue(item.Slug)
			info.DLCs = append(info.DLCs, model.DLCInfo{
				ID:    dlcID,
				Slug:  dlcSlug,
				Title: item.Title,
			})
		}
	}

	return info, nil
}

func readStringOrNumber(v jsontext.Value) (string, error) {
	if len(v) == 0 {
		return "", nil
	}
	dec := jsontext.NewDecoder(bytes.NewReader(v))
	tok, err := dec.ReadToken()
	if err != nil {
		return "", err
	}
	switch tok.Kind() {
	case jsontext.KindNull:
		return "", nil
	case jsontext.KindString, jsontext.KindNumber:
		return tok.String(), nil
	default:
		return "", fmt.Errorf("expected string or number, got %s", tok.Kind())
	}
}

func readStringValue(v jsontext.Value) (string, error) {
	if len(v) == 0 {
		return "", nil
	}
	dec := jsontext.NewDecoder(bytes.NewReader(v))
	tok, err := dec.ReadToken()
	if err != nil {
		return "", err
	}
	switch tok.Kind() {
	case jsontext.KindNull:
		return "", nil
	case jsontext.KindString:
		return tok.String(), nil
	default:
		return "", fmt.Errorf("expected string, got %s", tok.Kind())
	}
}

func cleanReleaseDate(date string) string {
	date = strings.TrimSpace(date)
	day, _, _ := strings.Cut(date, "T")
	return day
}

func normalizeImageURL(u string) string {
	u = strings.TrimSpace(u)
	if strings.HasPrefix(u, "//") {
		return "https:" + u
	}
	return u
}

func parseDescription(v jsontext.Value) string {
	if len(v) == 0 || v.Kind() == jsontext.KindNull {
		return ""
	}
	if v.Kind() == jsontext.KindString {
		var s string
		_ = jsonv2.Unmarshal(v, &s)
		return stripHTML(s)
	}
	if v.Kind() == jsontext.KindBeginObject {
		var desc struct {
			Lead string `json:"lead"`
			Full string `json:"full"`
		}
		_ = jsonv2.Unmarshal(v, &desc)
		if desc.Lead != "" {
			return stripHTML(desc.Lead)
		}
		return stripHTML(desc.Full)
	}
	return ""
}

func parseNameList(v jsontext.Value) []string {
	if len(v) == 0 || v.Kind() == jsontext.KindNull {
		return nil
	}
	if v.Kind() != jsontext.KindBeginArray {
		return nil
	}
	// Can be ["action", "rpg"] or [{"name": "action"}, {"name": "rpg"}]
	var strList []string
	if err := jsonv2.Unmarshal(v, &strList); err == nil {
		return strList
	}
	var objList []struct {
		Name string `json:"name"`
	}
	if err := jsonv2.Unmarshal(v, &objList); err == nil {
		var res []string
		for _, o := range objList {
			if o.Name != "" {
				res = append(res, o.Name)
			}
		}
		return res
	}
	return nil
}

func stripHTML(s string) string {
	s = strings.ReplaceAll(s, "<br>", "\n")
	s = strings.ReplaceAll(s, "<br/>", "\n")
	s = strings.ReplaceAll(s, "<br />", "\n")
	s = strings.ReplaceAll(s, "</p>", "\n\n")

	var buf strings.Builder
	inTag := false
	for i := 0; i < len(s); i++ {
		b := s[i]
		switch b {
		case '<':
			inTag = true
		case '>':
			inTag = false
		default:
			if !inTag {
				buf.WriteByte(b)
			}
		}
	}
	return strings.TrimSpace(buf.String())
}
