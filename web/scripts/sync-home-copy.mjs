import { readFileSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const webRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const site = JSON.parse(readFileSync(join(webRoot, 'content/site.json'), 'utf8'));
const path = join(webRoot, 'design-source/13-public-home-v4.dc.html');
const source = readFileSync(path, 'utf8');
const check = process.argv.includes('--check');

const values = {
  eyebrow: site.home.eyebrow,
  title: site.home.title,
  lead: site.home.lead,
  support: site.home.support,
  'operating-loop-title': site.home.operatingLoopTitle,
  'product-boundary': site.product.boundary,
};
site.operatingLoop.forEach(([code, body], index) => {
  values[`operating-loop-${index}-code`] = code;
  values[`operating-loop-${index}-body`] = body;
});
site.home.sections.forEach(({ id, title, body }) => {
  values[`section-${id}-title`] = title;
  values[`section-${id}-body`] = body;
});
const escapeHTML = (value) => String(value)
  .replaceAll('&', '&amp;')
  .replaceAll('<', '&lt;')
  .replaceAll('>', '&gt;');

let next = source;
for (const [key, value] of Object.entries(values)) {
  const start = `<!-- A2A_HOME_COPY:${key}:START -->`;
  const end = `<!-- A2A_HOME_COPY:${key}:END -->`;
  const from = next.indexOf(start);
  const to = next.indexOf(end, from + start.length);
  if (from < 0 || to < 0) throw new Error(`home copy: markers missing for ${key}`);
  if (next.indexOf(start, from + start.length) >= 0 || next.indexOf(end, to + end.length) >= 0) {
    throw new Error(`home copy: markers duplicated for ${key}`);
  }
  next = `${next.slice(0, from + start.length)}${escapeHTML(value)}${next.slice(to)}`;
}

if (check && next !== source) {
  throw new Error('home copy: public HTML drifted from web/content/site.json; run npm run sync:home-copy');
}
if (!check && next !== source) writeFileSync(path, next);
console.log(`home copy: ${check ? 'parity ok' : 'synchronized'} (${Object.keys(values).length} fields)`);
