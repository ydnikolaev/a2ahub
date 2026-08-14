import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

import { projectDemo } from '../src/lib/demo-projection.ts';

const demo = JSON.parse(readFileSync(new URL('../src/generated/demo.json', import.meta.url), 'utf8'));

test('public routes receive only the carried demo fields they render', () => {
  const changelog = projectDemo(demo, 'changelog');
  assert.deepEqual(Object.keys(changelog), ['meta', 'releaseNotes']);
  assert.strictEqual(changelog.meta, demo.meta);
  assert.strictEqual(changelog.releaseNotes, demo.releaseNotes);

  const dashboardExample = projectDemo(demo, 'dashboard-example');
  assert.deepEqual(Object.keys(dashboardExample), [
    'meta', 'generatedAt', 'self', 'tooling', 'spaces', 'nodes',
    'contractEdges', 'exchangeEdges', 'inbox', 'outbox', 'artifactDetails',
    'flags', 'unavailable', 'windows', 'vocabulary'
  ]);
  for (const key of Object.keys(dashboardExample)) assert.strictEqual(dashboardExample[key], demo[key], key);

  assert.strictEqual(projectDemo(demo, 'full'), demo);
});

test('unknown demo projections fail the build instead of shipping an accidental full fixture', () => {
  assert.throws(() => projectDemo(demo, 'typo'), /unknown demo projection "typo"/);
});
