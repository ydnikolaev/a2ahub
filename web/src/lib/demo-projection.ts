export const demoProjectionFields = Object.freeze({
  homepage: Object.freeze([
    'meta', 'generatedAt', 'self', 'tooling', 'spaces', 'nodes',
    'contractEdges', 'exchangeEdges', 'inbox', 'outbox', 'contracts'
  ]),
  changelog: Object.freeze(['meta', 'releaseNotes']),
  'dashboard-example': Object.freeze([
    'meta', 'generatedAt', 'self', 'tooling', 'spaces', 'nodes',
    'contractEdges', 'exchangeEdges', 'inbox', 'outbox', 'artifactDetails',
    'flags', 'unavailable', 'windows', 'vocabulary'
  ])
});

export function projectDemo(demo, projection = 'full') {
  if (projection === 'full') return demo;
  const fields = demoProjectionFields[projection];
  if (!fields) throw new Error(`unknown demo projection "${projection}"`);
  return Object.fromEntries(fields.map((field) => [field, demo[field]]));
}
