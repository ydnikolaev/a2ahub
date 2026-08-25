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

// Emphasis is design vocabulary, not copy, so site.json names a tone and this
// script owns the style string. A copy field stays a plain string for every
// other consumer — `description={site.home.lead}` in index.astro and the
// markdown/llms projections in generate-content.mjs read the same value.
const PILL_TONES = {
  teal: 'background:var(--teal-tint); color:var(--teal-strong-ink);',
  healthy: 'background:var(--healthy-tint); color:var(--healthy-strong);',
  attention: 'background:var(--attention-tint); color:var(--attention-strong);',
};
const LEAD_TONES = {
  plain: '',
  teal: ' style="color:var(--teal-strong-ink);"',
  healthy: ' style="color:var(--healthy-strong);"',
  attention: ' style="color:var(--attention-strong);"',
};
const renderEmphasis = ({ style, tone }, text) => {
  if (style === 'pill') {
    if (!PILL_TONES[tone]) throw new Error(`home copy: unknown pill tone ${tone}`);
    return `<span style="display:inline-block; padding:1px 7px; border-radius:6px; ${PILL_TONES[tone]} font-weight:600;">${text}</span>`;
  }
  if (style === 'lead') {
    if (LEAD_TONES[tone] === undefined) throw new Error(`home copy: unknown lead tone ${tone}`);
    return `<strong${LEAD_TONES[tone]}>${text}</strong>`;
  }
  throw new Error(`home copy: unknown emphasis style ${style}`);
};

// A highlight addresses its text by substring, so an ambiguous or absent match
// is a silent wrong-place wrap. Both are hard errors, the same shape as the
// marker throws above: the mechanism guards itself or it is not worth having.
const highlights = site.homeHighlights ?? {};
for (const key of Object.keys(highlights)) {
  if (!(key in values)) throw new Error(`home copy: highlight targets unknown field ${key}`);
}
const emphasize = (key, escaped) => {
  const spans = highlights[key];
  if (!spans?.length) return escaped;
  const hits = spans.map((span) => {
    const needle = escapeHTML(span.text);
    const at = escaped.indexOf(needle);
    if (at < 0) throw new Error(`home copy: highlight "${span.text}" is not in ${key}`);
    if (escaped.indexOf(needle, at + needle.length) >= 0) {
      throw new Error(`home copy: highlight "${span.text}" is ambiguous in ${key}`);
    }
    return { at, end: at + needle.length, span, needle };
  }).sort((a, b) => a.at - b.at);
  for (let i = 1; i < hits.length; i += 1) {
    if (hits[i].at < hits[i - 1].end) {
      throw new Error(`home copy: highlights overlap in ${key}`);
    }
  }
  let out = '';
  let cursor = 0;
  for (const hit of hits) {
    out += escaped.slice(cursor, hit.at) + renderEmphasis(hit.span, hit.needle);
    cursor = hit.end;
  }
  return out + escaped.slice(cursor);
};

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
  next = `${next.slice(0, from + start.length)}${emphasize(key, escapeHTML(value))}${next.slice(to)}`;
}

if (check && next !== source) {
  throw new Error('home copy: public HTML drifted from web/content/site.json; run npm run sync:home-copy');
}
if (!check && next !== source) writeFileSync(path, next);
console.log(`home copy: ${check ? 'parity ok' : 'synchronized'} (${Object.keys(values).length} fields)`);
