export const demoProjectionFields = Object.freeze({
  homepage: Object.freeze([
    'meta', 'generatedAt', 'self', 'tooling', 'spaces', 'nodes',
    'contractEdges', 'exchangeEdges', 'inbox', 'outbox', 'contracts', 'vocabulary'
  ]),
  changelog: Object.freeze(['meta', 'releaseNotes']),
  'dashboard-example': Object.freeze([
    'meta', 'generatedAt', 'self', 'tooling', 'spaces', 'nodes',
    'contractEdges', 'exchangeEdges', 'inbox', 'outbox', 'artifactDetails',
    'flags', 'unavailable', 'windows', 'vocabulary'
  ])
});

// The full catalogue is 96 entries — 9 KiB gzip, against a 35 KiB HTML budget
// the homepage already spends 25 KiB of. A page that resolves one family does
// not need the other eleven, so a projection may narrow the catalogue to the
// families its own component graph looks up. The `unknown` fallback always
// survives: a narrowed catalogue must still be able to say "not in the
// catalogue" rather than render an empty label.
//
// This list is a DECLARATION, and a declaration drifts. `vocabulary-projection`
// in tests/design-source.test.mjs recomputes the families from the page's
// transitive dc-import graph and fails when they disagree, so a component that
// starts resolving a new family cannot silently fall back to "Unknown value" —
// which is exactly how the homepage map lost every label between 2026-08-14
// and 2026-08-25.
export const demoVocabularyFamilies = Object.freeze({
  homepage: Object.freeze(['dependency-drift'])
});

function narrowVocabulary(vocabulary, families) {
  if (!vocabulary || !Array.isArray(vocabulary.entries)) return vocabulary;
  const keep = new Set(families);
  return { ...vocabulary, entries: vocabulary.entries.filter((entry) => keep.has(entry && entry.family)) };
}

export function projectDemo(demo, projection = 'full') {
  if (projection === 'full') return demo;
  const fields = demoProjectionFields[projection];
  if (!fields) throw new Error(`unknown demo projection "${projection}"`);
  const projected = Object.fromEntries(fields.map((field) => [field, demo[field]]));
  const families = demoVocabularyFamilies[projection];
  if (families && projected.vocabulary) projected.vocabulary = narrowVocabulary(projected.vocabulary, families);
  return projected;
}
