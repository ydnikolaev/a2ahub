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

test('the public changelog renders embedded releases before hydration', () => {
  const { logic } = runtimeDesignPage('15-changelog-v4.dc.html');

  assert.match(logic, /const INDEX = window\.A2A_RELEASE_INDEX \|\| \[/);
  assert.match(logic, /state = \{ data: globalThis\.A2A_DEMO \|\| null,/);
  assert.match(logic, /if \(this\.state\.data\) return;/);
  assert.match(logic, /isLatest: INDEX\.length > 0 && r\.version === INDEX\[0\]\[0\]/);
});

test('the public runtime hides raw design markup without collapsing its shell', () => {
  const source = readFileSync(new URL('../src/styles/site.css', import.meta.url), 'utf8');

  assert.match(source, /x-dc \{ display: block; min-height: 100vh; visibility: hidden; \}/);
  assert.match(source, /#dc-root:empty \{ min-height: 100vh; \}/);
});
