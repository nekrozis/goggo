package core

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/nekrozis/goggo/internal/util"
)

// defaultLanguageRequest is what a plan asks for when --language is absent. It
// goes through the same resolution as an explicit request, so a build with no
// English content reports that instead of silently installing another language.
const defaultLanguageRequest = "en"

// ErrLanguageNotOffered reports a --language value the selected build cannot
// satisfy. It is the one argument failure the front end cannot judge while
// parsing — the build is not known yet — so it travels back to the CLI to be
// reported as a usage error rather than as a failed command.
var ErrLanguageNotOffered = errors.New("language not offered by the build")

// LanguageError names the request and the tokens the build does offer, so the
// message can be acted on without a second command.
type LanguageError struct {
	Requested string
	ProductID string
	// Offered is a slice, so its pointer word is followed by the non-pointer
	// length and capacity: it comes last to keep the struct's GC-scanned
	// prefix at its shortest.
	Offered []string
}

func (e *LanguageError) Error() string {
	list := "(none)"
	if len(e.Offered) > 0 {
		list = strings.Join(e.Offered, ", ")
	}
	msg := fmt.Sprintf("language %q is not one of this build's languages\nAvailable languages: %s",
		e.Requested, list)
	if e.ProductID != "" {
		msg += fmt.Sprintf("\nUse 'goggo install options %s' to view available combinations", e.ProductID)
	}
	return msg
}

func (e *LanguageError) Unwrap() error { return ErrLanguageNotOffered }

// requestedLanguage is the language the run asks for: the --language value, or
// the default request when the flag was absent. A "present but empty" value is
// impossible — the option requires a value — so the empty string is the flag's
// absence and nothing else.
func requestedLanguage(req InstallRequest) string {
	if req.Language != "" {
		return req.Language
	}
	return defaultLanguageRequest
}

// offeredLanguages is the distinct language tokens a build's base depots
// declare, sorted. A wildcard depot declares no language and contributes
// nothing, which is what lets a language-agnostic build be recognised as one.
func offeredLanguages(candidates []depotCandidate) []string {
	seen := make(map[string]bool)
	var offered []string
	for _, c := range candidates {
		for _, l := range c.languages {
			if l == "*" {
				continue
			}
			if n := util.NormalizeLanguage(l); !seen[n] {
				seen[n] = true
				offered = append(offered, l)
			}
		}
	}
	sort.Strings(offered)
	return offered
}

// resolveLanguageTokens maps a language request onto the build's own tokens.
//
// The candidate set is what the build's base depots declare, so what
// `install options` lists is exactly what a request may name. A request matches
// by normalized exact token or by language family (util.LanguageFamilyMatch),
// and every match is selected: "en" on a build offering en-US and en-GB selects
// both, and "zh" on one offering zh-Hans and zh-Hant selects both. Nothing is
// ever guessed — a request that matches nothing is an error naming the tokens
// the build does offer.
//
// A build whose base depots declare no specific token at all is
// language-agnostic. The request then resolves to the wildcard, which is the
// only thing that selects its content.
func resolveLanguageTokens(candidates []depotCandidate, requested, productID string) ([]string, error) {
	offered := offeredLanguages(candidates)
	if len(offered) == 0 {
		return []string{"*"}, nil
	}

	var matched []string
	for _, token := range offered {
		if util.LanguageFamilyMatch(requested, token) {
			matched = append(matched, token)
		}
	}
	if len(matched) == 0 {
		return nil, &LanguageError{Requested: requested, Offered: offered, ProductID: productID}
	}
	sort.Strings(matched)
	return matched, nil
}

// requestLanguage resolves a plan's language request against one build, which
// may be the previously installed one. It returns the tokens the depot filter
// selects by, plus the notice for a request that matched more than one language.
//
// The class of a zero match depends on who asked: an explicit --language is a
// usage failure, while the default request that no build can satisfy is the
// ordinary "no compatible content" failure, because the user asked for nothing
// wrong.
func requestLanguage(manifest map[string]any, req InstallRequest) (tokens []string, notice string, err error) {
	baseProductID, err := baseProductIDOf(manifest, req)
	if err != nil {
		return nil, "", err
	}
	candidates, err := depotCandidates(manifest, baseProductID)
	if err != nil {
		return nil, "", err
	}

	requested := requestedLanguage(req)
	tokens, err = resolveLanguageTokens(candidates, requested, req.ProductID)
	if err != nil {
		if req.Language != "" {
			return nil, "", err
		}
		return nil, "", noMatchingContent(req, requested)
	}
	if len(tokens) > 1 {
		notice = fmt.Sprintf("Language %q matches several languages of this build: %s; installing all of them",
			requested, strings.Join(tokens, ", "))
	}
	return tokens, notice, nil
}

// noMatchingContent is the diagnostic for a plan whose request selects no
// content, naming the tuple it was built from.
func noMatchingContent(req InstallRequest, requested string) error {
	return fmt.Errorf("%w matching platform=%s, language=%s, arch=%s\nUse 'goggo install options %s' to view available combinations",
		ErrNoMatchingContent, req.Platform, requested, archName(req.Arch), req.ProductID)
}
