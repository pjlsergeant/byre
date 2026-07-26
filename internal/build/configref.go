package build

import (
	"bytes"
	_ "embed"
	"fmt"
	"regexp"
)

// configRefDoc is the full config reference baked into every agent image at
// /etc/byre/config-reference.md, DERIVED from the site's configuration
// reference (site/content/docs/configuration-reference.md) by
// StripSiteReference -- one source, two renderings, so the in-box copy can
// never drift from the published one. The derived copy is checked in (embed
// cannot cross package boundaries); a test pins it to the live site file.
// Regenerate: go run ./cmd/byre config-reference-doc > internal/build/config-reference.md
//
//go:embed config-reference.md
var configRefDoc string

// siteLinkRe matches a markdown link to a site-relative /docs path. In the
// box the site isn't reachable (and a deny-by-default box couldn't fetch it
// anyway), so the link text is kept and the destination becomes a plain
// getbyre.com URL the agent can report to the user.
var siteLinkRe = regexp.MustCompile(`\[([^\]]+)\]\((/docs/[^)]*)\)`)

// StripSiteReference turns the site's configuration-reference page into the
// box-side document: front-matter dropped, site-relative links rewritten to
// plain getbyre.com URLs, and a short header naming what the file is.
// Deliberately dumb: a construct it doesn't recognize (a Hugo shortcode) is a
// loud error, not a guess -- a build failure is the correct behavior when the
// source grows a construct this rendering doesn't understand.
func StripSiteReference(src []byte) ([]byte, error) {
	if bytes.Contains(src, []byte("{{<")) || bytes.Contains(src, []byte("{{%")) {
		return nil, fmt.Errorf("site reference contains a Hugo shortcode; teach StripSiteReference about it before baking")
	}
	// Front-matter: the block between the leading `---` pair. Required, not
	// optional: this strips ONE known site artifact, and a source that lost
	// its opening delimiter (or grew a BOM) should fail regeneration loudly,
	// not bake half-stripped metadata.
	if !bytes.HasPrefix(src, []byte("---\n")) {
		return nil, fmt.Errorf("site reference does not open with front-matter; is this the right file?")
	}
	rest := src[4:]
	end := bytes.Index(rest, []byte("\n---\n"))
	if end < 0 {
		return nil, fmt.Errorf("site reference front-matter never closes")
	}
	body := rest[end+5:]
	body = siteLinkRe.ReplaceAll(body, []byte("$1 (https://getbyre.com$2)"))
	head := "# byre configuration reference\n\n" +
		"The complete config vocabulary, as published at\n" +
		"https://getbyre.com/docs/configuration-reference/ -- baked into this box\n" +
		"so it is readable offline.\n\n"
	return append([]byte(head), bytes.TrimLeft(body, "\n")...), nil
}
