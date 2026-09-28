package core

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/nekrozis/goggo/internal/config"
)

// dlcProducts collects the DLC products the selected build carries: every depot
// whose productId differs from the base product's, sorted and deduplicated.
//
// It is the one discovery path. The install-options listing, the selectors and
// the plan's own DLC handling all read it, so a product the listing shows is
// exactly one --dlc accepts. It reads the depots rather than the product
// document's `dlcs` field, which is editorial and has been measured to disagree
// with what a build actually ships.
func dlcProducts(manifest map[string]any, baseProductID string) ([]string, error) {
	depots, err := manifestArray(manifest, "depots")
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	var ids []string
	for i, raw := range depots {
		depot, err := mapObject(raw)
		if err != nil {
			return nil, fmt.Errorf("galaxy: manifest depots[%d]: %w", i, err)
		}
		productID, err := scalarString(depot["productId"])
		if err != nil || productID == "" || productID == baseProductID {
			continue
		}
		if seen[productID] {
			continue
		}
		seen[productID] = true
		ids = append(ids, productID)
	}
	sort.Strings(ids)
	return ids, nil
}

// dlcTitles reads the display title of every given DLC product.
//
// A product whose document cannot be read keeps an empty title rather than
// failing the run: the product id taken from the depots is the fact the
// selection works from, and a title is only one of the two ways a user may name
// it. The listing therefore always shows the id, and a title when it answered.
func (d *Downloader) dlcTitles(ctx context.Context, products []string) map[string]string {
	titles := make(map[string]string, len(products))
	for _, id := range products {
		doc, err := d.galaxy.Product(ctx, id)
		if err != nil {
			continue
		}
		titles[id] = doc.Title
	}
	return titles
}

// dlcListing is the DLC section of an options report: what the build carries,
// what each one is called, and whether the account owns it. Ownership comes from
// the resolver the transfer consults anyway, so the listing and an install can
// never disagree about what is installable.
func (d *Downloader) dlcListing(ctx context.Context, manifest map[string]any, baseProductID string) ([]DLCOption, error) {
	products, err := dlcProducts(manifest, baseProductID)
	if err != nil {
		return nil, err
	}
	if len(products) == 0 {
		return nil, nil
	}

	titles := d.dlcTitles(ctx, products)
	dlcs := make([]DLCOption, 0, len(products))
	for _, id := range products {
		res, err := d.links.product(ctx, id)
		if err != nil {
			return nil, err
		}
		dlcs = append(dlcs, DLCOption{ProductID: id, Title: titles[id], Owned: res.owned})
	}
	return dlcs, nil
}

// dlcName is the comparison form of a DLC title: case is ignored and surrounding
// space is trimmed. The interior is left alone — a title is a name, not a
// language tag, so folding runs of spaces would accept spellings nobody typed.
func dlcName(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// dlcDisplayName names a resolved DLC the way the user can recognise it: its
// title, or its product id when the document did not answer.
func dlcDisplayName(id string, titles map[string]string) string {
	if title := strings.TrimSpace(titles[id]); title != "" {
		return title
	}
	return id
}

// dlcSelection is the --dlc / --exclude-dlc request resolved against one build.
//
// The zero value is the documented default: no selector was given, so every
// licensed DLC is installed. only is nil exactly when no --dlc was given, which
// is what distinguishes "install all of them" from "install none of them".
type dlcSelection struct {
	only     map[string]bool
	notThese map[string]bool
}

// keeps reports whether a DLC product survives the selection. The base product
// is not a DLC and never consults it.
func (s dlcSelection) keeps(productID string) bool {
	if s.notThese[productID] {
		return false
	}
	if s.only != nil && !s.only[productID] {
		return false
	}
	return true
}

// selecting reports whether any --dlc was given, i.e. whether the selection
// narrows the default rather than only subtracting from it.
func (s dlcSelection) selecting() bool { return s.only != nil }

// dlcProblem is one refused selector with the input position it came from, so
// the collected report keeps the order the user typed. The string comes first
// because its pointer word is followed by the non-pointer length: keeping it
// first keeps the struct's GC-scanned prefix at its shortest.
type dlcProblem struct {
	text string
	at   int
}

// resolveDLCSelection maps the selector values onto the build's DLC products.
//
// A selector matches by exact product id, or by the full DLC title normalized
// (case-insensitive, surrounding space trimmed). Nothing else: a substring match
// would make ordinary words ambiguous, and guessing would install something the
// user did not ask for. Two DLCs sharing a title are ambiguous by definition.
//
// Include and exclude are not symmetric, because the intent is not. A --dlc is a
// promise — install this — so an unowned one is refused rather than silently
// skipped, which would report success for a DLC that is not there. An
// --exclude-dlc naming an unowned DLC is already satisfied by the entitlement
// filter, so it is a no-op.
//
// Every problem is collected and reported together, in selector input order: the
// run has not started, so the user can fix all of them in one edit.
func (d *Downloader) resolveDLCSelection(ctx context.Context, manifest map[string]any, baseProductID, productID string, selectors []config.DLCSelector) (dlcSelection, error) {
	if len(selectors) == 0 {
		return dlcSelection{}, nil
	}

	products, err := dlcProducts(manifest, baseProductID)
	if err != nil {
		return dlcSelection{}, err
	}
	ids := make(map[string]bool, len(products))
	for _, id := range products {
		ids[id] = true
	}

	// Titles are read only when a selector is not a product id of this build, or
	// when a problem has to be named: selecting by id needs no title, and neither
	// does the default scan.
	var titles map[string]string
	loadTitles := func() map[string]string {
		if titles == nil {
			titles = d.dlcTitles(ctx, products)
		}
		return titles
	}
	for _, s := range selectors {
		if !ids[s.Value] {
			loadTitles()
			break
		}
	}

	sel := dlcSelection{notThese: map[string]bool{}}
	only := map[string]bool{}
	var problems []dlcProblem

	for at, s := range selectors {
		id, problem := resolveDLCSelector(s.Value, products, ids, titles)
		if problem != "" {
			problems = append(problems, dlcProblem{at: at, text: problem})
			continue
		}
		if s.Exclude {
			sel.notThese[id] = true
			continue
		}
		only[id] = true
		// The entitlement oracle the transfer will consult anyway: a DLC the
		// account does not own cannot be installed, so the promise is refused
		// before anything is written.
		res, err := d.links.product(ctx, id)
		if err != nil {
			return dlcSelection{}, err
		}
		if !res.owned {
			problems = append(problems, dlcProblem{at: at, text: dlcNotOwned(id, loadTitles())})
		}
	}

	if len(only) > 0 {
		sel.only = only
	}
	// Contradiction: one DLC named both ways. Detected on the resolved id, so an
	// id and a title naming the same DLC still conflict. It is reported at the
	// position of the --dlc that asked for it.
	for at, s := range selectors {
		if s.Exclude {
			continue
		}
		id, problem := resolveDLCSelector(s.Value, products, ids, titles)
		if problem == "" && sel.notThese[id] {
			problems = append(problems, dlcProblem{at: at, text: fmt.Sprintf(
				"%q is both selected and excluded", dlcDisplayName(id, loadTitles()))})
		}
	}

	if len(problems) > 0 {
		sort.SliceStable(problems, func(i, j int) bool { return problems[i].at < problems[j].at })
		texts := make([]string, 0, len(problems))
		for _, p := range problems {
			texts = append(texts, "  "+p.text)
		}
		msg := fmt.Sprintf("cannot use the selected DLCs:\n\n%s", strings.Join(texts, "\n"))
		if productID != "" {
			msg += fmt.Sprintf("\n\nUse 'goggo install options %s' to view the DLCs of this build", productID)
		}
		return dlcSelection{}, Usagef("%s", msg)
	}
	return sel, nil
}

// dlcNotOwned is the problem for a selected DLC the account does not own. The
// product id is shown only when it is not already the name the user would see,
// so an untitled DLC is not reported as '"123" ... (product 123)'.
func dlcNotOwned(id string, titles map[string]string) string {
	name := dlcDisplayName(id, titles)
	if name == id {
		return fmt.Sprintf("%q is not owned by this account", name)
	}
	return fmt.Sprintf("%q is not owned by this account (product %s)", name, id)
}

// resolveDLCSelector maps one selector onto a DLC product id, or returns the
// problem text for a value that names none or several.
func resolveDLCSelector(value string, products []string, ids map[string]bool, titles map[string]string) (id, problem string) {
	if ids[value] {
		return value, ""
	}

	want := dlcName(value)
	var matched []string
	for _, candidate := range products {
		title := strings.TrimSpace(titles[candidate])
		if title != "" && dlcName(title) == want {
			matched = append(matched, candidate)
		}
	}
	switch len(matched) {
	case 0:
		return "", fmt.Sprintf("%q is not a DLC of this build", value)
	case 1:
		return matched[0], ""
	default:
		lines := make([]string, 0, len(matched))
		for _, candidate := range matched {
			lines = append(lines, fmt.Sprintf("      %s (product %s)", titles[candidate], candidate))
		}
		return "", fmt.Sprintf("%q is ambiguous; candidates:\n%s", value, strings.Join(lines, "\n"))
	}
}
