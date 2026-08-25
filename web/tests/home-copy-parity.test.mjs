import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import test from 'node:test';

const webFile = path => readFileSync(new URL(`../${path}`, import.meta.url), 'utf8');

test('public homepage prose is generated from site.json', () => {
  execFileSync(process.execPath, ['scripts/sync-home-copy.mjs', '--check'], {
    cwd: new URL('..', import.meta.url),
    stdio: 'pipe'
  });

  const site = JSON.parse(webFile('content/site.json'));
  const source = webFile('design-source/13-public-home-v4.dc.html');
  const expectedMarkers = [
    'eyebrow', 'title', 'lead', 'support', 'operating-loop-title', 'product-boundary',
    ...site.operatingLoop.flatMap((_, index) => [`operating-loop-${index}-code`, `operating-loop-${index}-body`]),
    ...site.home.sections.flatMap(section => [`section-${section.id}-title`, `section-${section.id}-body`]),
  ].sort();
  const actualMarkers = [...source.matchAll(/<!-- A2A_HOME_COPY:([^:]+):START -->/g)]
    .map(match => match[1])
    .sort();
  assert.deepEqual(actualMarkers, expectedMarkers, 'every load-bearing site.home field must have one source marker');
});

// The hero lost its highlighted words on 2026-08-04, and nothing noticed for
// three weeks: the copy SSOT escapes HTML, so the inline emphasis that lived in
// the design source was flattened the first time the sync ran and the parity
// check stayed green over the flattened result. Emphasis is now declared in
// site.json and rendered by the sync, and this is what makes deleting a
// declaration — or the renderer — fail out loud instead of silently.
test('declared emphasis actually renders into the public homepage', () => {
  const site = JSON.parse(webFile('content/site.json'));
  const source = webFile('design-source/13-public-home-v4.dc.html');
  const highlights = site.homeHighlights ?? {};

  assert.ok(Object.keys(highlights).length > 0, 'the homepage declares at least one highlight');
  assert.ok(highlights.lead?.length >= 1, 'the hero lead keeps its highlighted words');

  for (const [key, spans] of Object.entries(highlights)) {
    const region = source.match(
      new RegExp(`<!-- A2A_HOME_COPY:${key}:START -->([\\s\\S]*?)<!-- A2A_HOME_COPY:${key}:END -->`)
    );
    assert.ok(region, `${key} has a copy marker to render into`);
    for (const span of spans) {
      const text = span.text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
      const element = span.style === 'pill'
        ? new RegExp(`<span style="[^"]*">${text}</span>`)
        : new RegExp(`<strong(?: style="[^"]*")?>${text}</strong>`);
      assert.match(region[1], element, `${key} renders "${span.text}" as ${span.style} emphasis`);
    }
  }
});

test('Features source metadata describes explicit agent runs on every BaseLayout projection', () => {
  const routes = JSON.parse(webFile('content/routes.json')).routes;
  const description = routes.find(route => route.id === 'features').description;
  const layout = webFile('src/layouts/BaseLayout.astro');

  assert.match(description, /explicit agent runs/i);
  assert.doesNotMatch(description, /autonomous workflows/i);
  assert.match(layout, /<meta name="description" content=\{description\}/);
  assert.match(layout, /<meta property="og:description" content=\{description\}/);
  assert.match(layout, /<meta name="twitter:description" content=\{description\}/);
  assert.match(layout, /name: title,\s+description,\s+isPartOf:/);
});
