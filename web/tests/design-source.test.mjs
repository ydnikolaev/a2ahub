import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { runtimeDesignPage } from '../src/lib/design-source.ts';

test('the public Features projection contains only the dashboard Guide screen', () => {
  const { template } = runtimeDesignPage('14-local-dashboard-v4.dc.html', 'guide');

  assert.match(template, /data-screen-label="Guide"/);
  assert.doesNotMatch(template, /aria-label="\{\{ guideAria \}\}"/);
  assert.doesNotMatch(template, /toggleTypeMenu|integrityFlagsLabel|data-screen-label="Overview"/);
});

test('the shared segmented filter bounds wide options inside a horizontal scroller', () => {
  const source = readFileSync(new URL('../design-source/SegmentedFilter.dc.html', import.meta.url), 'utf8');

  assert.match(source, /max-width:calc\(100vw - 56px\)/);
  assert.match(source, /overflow-x:auto/);
  assert.match(source, /width:max-content/);
  assert.match(source, /flex:0 0 auto/);
});
