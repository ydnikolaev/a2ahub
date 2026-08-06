// POL-005's message. It lives in its own file because three call sites
// (manifest.go, consumes.go, engine.go) raise the identical refusal for three
// families, and three copies of a sentence is how one of them gets improved and
// the other two do not.
package validate

import (
	"fmt"

	"github.com/ydnikolaev/a2ahub/internal/schema"
)

// unreadableOrUnacceptedSchemaMessage renders POL-005 for one family, naming
// the accepted `schema:` value and the remedy — and distinguishing the two
// causes that used to share one sentence.
//
// The old message was "…schema version is outside the one-cycle overlap window
// this binary understands", for BOTH a value that does not parse as `<family>/
// v<N>` at all and a value that parses but names an unsupported version. Those
// have opposite remedies: the first is a typo the author fixes in place, the
// second means the document was written by a newer a2a and the reader must
// upgrade. Telling someone with a typo about an overlap window sends them to
// look for a version problem they do not have — and the strings "one-cycle
// overlap window" and "schema version is outside" had zero hits across the
// shipped skill manual and the published docs, so there was nowhere to go and
// find out. Compare POL-012 in this same package, which has always said
// "update a2a and author the event again".
//
// Both families whose current version is 1 (space, consumes) have never shipped
// a v2, so in practice this fires today on a malformed value rather than real
// version skew — which is precisely the case the old wording served worst.
func unreadableOrUnacceptedSchemaMessage(subject, family, found string) string {
	accepted := schema.AcceptedVersions(family)
	if _, parsed := schema.ParseVersion(found); !parsed {
		return fmt.Sprintf(
			"%s declares schema: %q, which is not a %s schema version. Set it to %s.",
			subject, found, family, accepted)
	}
	return fmt.Sprintf(
		"%s declares schema: %q, which this build of a2a does not accept for authoring; it accepts %s. "+
			"A newer version here means the document was written by a newer a2a — run `a2a update`. "+
			"Otherwise correct the field.",
		subject, found, accepted)
}
