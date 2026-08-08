package fold

import "testing"

// TestEveryNamedTargetIsAuthorized is P-2 at the level it is violated.
//
// `to` carries `minItems: 1` and NO `maxItems` on the base schema; only the
// four exchange types add one. So a requirement, contract, decision or
// announcement may legally name several targets — and `roleAuthorizes`
// compared the actor against `env.To0()` alone, authorizing the FIRST named
// system and refusing every other.
//
// That is not a cosmetic asymmetry. internal/pendency's `target()` returns
// EVERY entry, so one `a2a thread --json` object carried `waiting_on` naming
// three systems and `next_actions` naming one, with `your_move: true` going
// to systems whose every move the fold would reject. The surface tells two
// of the three to act and the domain refuses them.
func TestEveryNamedTargetIsAuthorized(t *testing.T) {
	t.Parallel()

	env := Envelope{
		ID:   "XR-acme-multi",
		Kind: KindRequirement,
		From: "acme",
		To:   []string{"beta", "gamma", "delta"},
	}

	for _, system := range env.To {
		if !roleAuthorizes(RoleTarget, env, system) {
			t.Errorf("%q is named in `to` and RoleTarget refuses it — pendency names every entry as owing the move, so a target the fold will not accept is a surface promising an act the domain rejects", system)
		}
	}
}

// TestANonTargetIsStillRefused keeps the widening honest: authorizing every
// NAMED target must not authorize anyone else.
func TestANonTargetIsStillRefused(t *testing.T) {
	t.Parallel()

	env := Envelope{
		ID:   "XR-acme-multi",
		Kind: KindRequirement,
		From: "acme",
		To:   []string{"beta", "gamma"},
	}
	for _, system := range []string{"acme", "epsilon", ""} {
		if roleAuthorizes(RoleTarget, env, system) {
			t.Errorf("%q is not named in `to` and RoleTarget accepted it", system)
		}
	}
}

// TestEveryNamedPartyMayNote is the asymmetry a post-P1 audit found. The
// RoleTarget widening shipped without its RoleEitherParty counterpart, so on
// a requirement addressed to three systems the second and third could
// acknowledge or decline — state-moving acts — and were refused when
// attaching a `note` to the same artifact.
//
// D-025's note is the only consumer of RoleEitherParty, which is why nothing
// else caught it: the narrowing was invisible everywhere except the one act
// it governed.
func TestEveryNamedPartyMayNote(t *testing.T) {
	t.Parallel()

	env := Envelope{
		ID:   "XR-acme-multi",
		Kind: KindRequirement,
		From: "acme",
		To:   []string{"beta", "gamma", "delta"},
	}

	for _, party := range append([]string{env.From}, env.To...) {
		if !roleAuthorizes(RoleEitherParty, env, party) {
			t.Errorf("%q is a party to this artifact and RoleEitherParty refuses it — the same system may acknowledge and may not comment, which cannot both be right", party)
		}
	}
	for _, outsider := range []string{"epsilon", ""} {
		if roleAuthorizes(RoleEitherParty, env, outsider) {
			t.Errorf("%q is not a party and RoleEitherParty accepted it", outsider)
		}
	}
}
