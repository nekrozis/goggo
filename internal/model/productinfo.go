package model

// ProductInfo represents the canonical metadata identity card for a GOG product.
type ProductInfo struct {
	ID          string    `json:"id"`
	Slug        string    `json:"slug"`
	Title       string    `json:"title"`
	Platforms   []string  `json:"platforms"`
	Icon        string    `json:"icon,omitempty"`
	Logo        string    `json:"logo,omitempty"`
	ReleaseDate string    `json:"release_date,omitempty"`
	Tags        []string  `json:"tags,omitempty"`
	Genres      []string  `json:"genres,omitempty"`
	DLCs        []DLCInfo `json:"dlcs,omitempty"`
	Description string    `json:"description,omitempty"`
}

// DLCInfo holds lightweight identity info for an associated DLC.
type DLCInfo struct {
	ID    string `json:"id"`
	Slug  string `json:"slug,omitempty"`
	Title string `json:"title"`
}
