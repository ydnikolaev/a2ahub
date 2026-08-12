import { existsSync, readFileSync, readdirSync } from 'node:fs';
import { extname, join, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { gzipSync } from 'node:zlib';

const webRoot = resolve(fileURLToPath(new URL('..', import.meta.url)));
const dist = join(webRoot, 'dist');
const manifest = JSON.parse(readFileSync(join(webRoot, 'src/generated/route-manifest.json'), 'utf8'));
const failures = [];
const canonicalOrigin = 'https://a2ahub.dev';
const kib = 1024;

function filesBelow(root) {
  return readdirSync(root, { withFileTypes: true }).flatMap((entry) => {
    const path = join(root, entry.name);
    return entry.isDirectory() ? filesBelow(path) : [path];
  });
}

function localTarget(value, sourceFile) {
  const clean = value.split('#', 1)[0].split('?', 1)[0];
  if (!clean || /^(?:[a-z]+:|\/\/)/i.test(clean)) return null;
  const rooted = clean.startsWith('/') ? clean.slice(1) : null;
  if (clean === '/') return join(dist, 'index.html');
  const target = rooted === null ? resolve(join(sourceFile, '..'), clean) : join(dist, rooted);
  if (extname(target)) return target;
  return join(target, 'index.html');
}

function routeFile(route) {
  return join(dist, route.path === '/' ? 'index.html' : route.path.replace(/^\//, ''));
}

function attribute(source, selector, name) {
  const tag = source.match(selector)?.[0] ?? '';
  return tag.match(new RegExp(`\\b${name}=["']([^"']+)["']`, 'i'))?.[1] ?? '';
}

function resourceBytes(source, pattern) {
  const urls = new Set([...source.matchAll(pattern)].map((match) => match[1]).filter((url) => url.startsWith('/_astro/')));
  return [...urls].reduce((total, url) => {
    const file = join(dist, url.slice(1));
    if (!existsSync(file)) {
      failures.push(`missing built resource ${url}`);
      return total;
    }
    return total + gzipSync(readFileSync(file)).length;
  }, 0);
}

for (const file of filesBelow(dist)) {
  const source = readFileSync(file, 'utf8');
  const name = relative(dist, file);
  const sourceWithoutApprovedAnalytics = source.replaceAll('https://www.googletagmanager.com/gtag/js?id=G-P4VH0QW93R', '');

  const isDesignResource = name.startsWith('design/');
  if (!isDesignResource && (/\b(?:src|srcset)=["']https?:\/\//i.test(sourceWithoutApprovedAnalytics) || /url\(\s*["']?https?:\/\//i.test(sourceWithoutApprovedAnalytics))) {
    failures.push(`${name}: automatic remote request`);
  }
  if (source.includes('ydnikolaev.github.io/a2ahub')) failures.push(`${name}: legacy canonical URL`);

  if (extname(file) === '.html' && !isDesignResource) {
    if (!source.includes('https://a2ahub.dev/')) failures.push(`${name}: missing a2ahub.dev canonical surface`);
    for (const match of source.matchAll(/\bhref=["']([^"']+)["']/gi)) {
      if (match[1].includes('{{')) continue;
      const target = localTarget(match[1], file);
      if (target && !existsSync(target)) failures.push(`${name}: broken local link ${match[1]}`);
    }
    for (const match of source.matchAll(/<script type="text\/x-dc"[^>]*>([\s\S]*?)<\/script>/gi)) {
      try {
        new Function(match[1]);
      } catch (error) {
        failures.push(`${name}: invalid Design Component logic (${error.message})`);
      }
    }
  }
}

for (const required of ['CNAME', 'llms.txt', 'llms-full.txt', 'sitemap.xml', 'robots.txt', 'site.webmanifest', 'feed.xml', '.well-known/security.txt', 'social-card.png', 'apple-touch-icon.png', 'setup/a2a.md', 'demo-data.json', 'install.sh']) {
  if (!existsSync(join(dist, required))) failures.push(`missing generated ${required}`);
}

if (readFileSync(join(dist, 'CNAME'), 'utf8').trim() !== 'a2ahub.dev') failures.push('CNAME is not a2ahub.dev');

const sitemap = readFileSync(join(dist, 'sitemap.xml'), 'utf8');
const sitemapURLs = [...sitemap.matchAll(/<loc>([^<]+)<\/loc>/g)].map((match) => match[1]);
const expectedSitemapURLs = manifest.routes.filter((route) => route.indexable).map((route) => `${canonicalOrigin}${route.canonical_path}`);
for (const url of expectedSitemapURLs) {
  if (sitemapURLs.filter((candidate) => candidate === url).length !== 1) failures.push(`sitemap.xml: expected canonical exactly once ${url}`);
}
for (const url of sitemapURLs) {
  if (!expectedSitemapURLs.includes(url)) failures.push(`sitemap.xml: unexpected or noindex URL ${url}`);
}

const seenTitles = new Map();
const seenDescriptions = new Map();
for (const route of manifest.routes) {
  const file = routeFile(route);
  if (!existsSync(file)) {
    failures.push(`${route.path}: route manifest target is missing`);
    continue;
  }
  const source = readFileSync(file, 'utf8');
  const name = relative(dist, file);
  const expectedCanonical = `${canonicalOrigin}${route.canonical_path}`;
  const canonical = attribute(source, /<link\b[^>]*\brel=["']canonical["'][^>]*>/i, 'href');
  const title = source.match(/<title>([^<]+)<\/title>/i)?.[1]?.trim() ?? '';
  const description = attribute(source, /<meta\b[^>]*\bname=["']description["'][^>]*>/i, 'content');
  const openGraphDescription = attribute(source, /<meta\b[^>]*\bproperty=["']og:description["'][^>]*>/i, 'content');
  const twitterDescription = attribute(source, /<meta\b[^>]*\bname=["']twitter:description["'][^>]*>/i, 'content');
  const robots = attribute(source, /<meta\b[^>]*\bname=["']robots["'][^>]*>/i, 'content');
  const analyticsCount = source.match(/G-P4VH0QW93R/g)?.length ?? 0;
  const jsonBlocks = [...source.matchAll(/<script\b[^>]*type=["']application\/ld\+json["'][^>]*>([\s\S]*?)<\/script>/gi)];

  if (canonical !== expectedCanonical) failures.push(`${name}: canonical ${canonical || 'missing'} does not match ${expectedCanonical}`);
  if (!title) failures.push(`${name}: missing title`);
  if (!description) failures.push(`${name}: missing description`);
  if (openGraphDescription !== description) failures.push(`${name}: Open Graph description drifted from the route description`);
  if (twitterDescription !== description) failures.push(`${name}: Twitter description drifted from the route description`);
  if (route.indexable) {
    if (!robots.startsWith('index, follow')) failures.push(`${name}: indexable route lacks index/follow robots policy`);
    if (analyticsCount !== 2) failures.push(`${name}: GA4 tag must be configured exactly once`);
    for (const marker of ['property="og:title"', 'property="og:description"', 'property="og:url"', 'property="og:image"', 'name="twitter:card"', 'name="twitter:image"']) {
      if (!source.includes(marker)) failures.push(`${name}: missing social metadata ${marker}`);
    }
    if (jsonBlocks.length !== 1) failures.push(`${name}: expected one JSON-LD graph`);
    for (const block of jsonBlocks) {
      try {
        const value = JSON.parse(block[1]);
        if (value['@context'] !== 'https://schema.org' || !Array.isArray(value['@graph']) || value['@graph'].length < 3) {
          failures.push(`${name}: incomplete JSON-LD graph`);
        }
        const page = value['@graph']?.find((entry) => entry['@id'] === `${expectedCanonical}#webpage`);
        if (page?.description !== description) failures.push(`${name}: JSON-LD description drifted from the route description`);
      } catch (error) {
        failures.push(`${name}: invalid JSON-LD (${error.message})`);
      }
    }
    if (!source.includes('class="skip-link" href="#main"') || !source.includes('id="main"')) failures.push(`${name}: missing skip-link/main target pair`);
    if ((source.match(/<h1\b/gi)?.length ?? 0) !== 1) failures.push(`${name}: expected exactly one H1`);

    const htmlBytes = gzipSync(Buffer.from(source)).length;
    const jsBytes = resourceBytes(source, /<script\b[^>]*\bsrc=["']([^"']+)["'][^>]*>/gi);
    const cssBytes = resourceBytes(source, /<link\b[^>]*\brel=["']stylesheet["'][^>]*\bhref=["']([^"']+)["'][^>]*>/gi);
    const htmlBudget = route.html_budget_kib ?? 35;
    if (htmlBytes > htmlBudget * kib) failures.push(`${name}: HTML ${Math.ceil(htmlBytes / kib)} KiB gzip exceeds ${htmlBudget} KiB`);
    if (jsBytes > (route.id === 'home' ? 150 : 80) * kib) failures.push(`${name}: JS ${Math.ceil(jsBytes / kib)} KiB gzip exceeds route budget`);
    if (cssBytes > 25 * kib) failures.push(`${name}: CSS ${Math.ceil(cssBytes / kib)} KiB gzip exceeds 25 KiB`);
  } else {
    if (!robots.startsWith('noindex, follow')) failures.push(`${name}: non-indexable route lacks noindex/follow`);
    if (analyticsCount !== 0) failures.push(`${name}: analytics must stay disabled`);
    if (sitemapURLs.includes(expectedCanonical)) failures.push(`${name}: noindex route appears in sitemap`);
  }
  if (route.id === 'features') {
    if (!/explicit agent runs/i.test(description)) failures.push(`${name}: Features metadata does not name the explicit-run boundary`);
    if (/autonomous workflows/i.test(source)) failures.push(`${name}: stale autonomous-workflows claim returned in built metadata`);
  }
  if (route.markdown) {
    const alternate = attribute(source, /<link\b[^>]*\brel=["']alternate["'][^>]*\btype=["']text\/markdown["'][^>]*>/i, 'href');
    const expectedAlternate = `${canonicalOrigin}${route.markdown}`;
    if (alternate !== expectedAlternate) failures.push(`${name}: Markdown alternate ${alternate || 'missing'} does not match ${expectedAlternate}`);
    if (!existsSync(join(dist, route.markdown.replace(/^\//, '')))) failures.push(`${name}: Markdown alternate target is missing`);
  }
  if (route.indexable && title) {
    if (seenTitles.has(title)) failures.push(`${name}: duplicate title also used by ${seenTitles.get(title)}`);
    seenTitles.set(title, name);
  }
  if (route.indexable && description) {
    if (seenDescriptions.has(description)) failures.push(`${name}: duplicate description also used by ${seenDescriptions.get(description)}`);
    seenDescriptions.set(description, name);
  }
}

const dashboard = readFileSync(join(dist, 'dashboard.html'), 'utf8');
if (!dashboard.includes('data-public-home-link') || !dashboard.includes('Back to home')) {
  failures.push('dashboard.html: missing public return-home link');
}

const robotsText = readFileSync(join(dist, 'robots.txt'), 'utf8');
for (const bot of ['OAI-SearchBot', 'GPTBot', 'ChatGPT-User', 'ClaudeBot', 'Claude-SearchBot', 'Claude-User', '*']) {
  if (!robotsText.includes(`User-agent: ${bot}\nAllow: /`)) failures.push(`robots.txt: missing explicit allow policy for ${bot}`);
}
if (!robotsText.includes(`Sitemap: ${canonicalOrigin}/sitemap.xml`)) failures.push('robots.txt: missing canonical sitemap URL');

const llms = readFileSync(join(dist, 'llms.txt'), 'utf8');
if ((llms.match(/^# /gm)?.length ?? 0) !== 1 || !llms.startsWith('# a2ahub\n\n> ')) failures.push('llms.txt: expected one H1 followed by a blockquote summary');
for (const heading of ['## Start here', '## Operate the protocol', '## Trust and verification', '## Optional']) {
  if (!llms.includes(heading)) failures.push(`llms.txt: missing ${heading}`);
}
for (const phrase of ['not an agent framework', 'Git-backed coordination infrastructure', 'One agent in one repository']) {
  if (!llms.includes(phrase)) failures.push(`llms.txt: missing product boundary phrase "${phrase}"`);
}
if (/dashboard\.html|dashboard\.md/.test(llms)) failures.push('llms.txt: raw dashboard fixture must not be recommended');
for (const match of llms.matchAll(/\[[^\]]+\]\((https:\/\/a2ahub\.dev\/[^)]+)\)/g)) {
  const url = new URL(match[1]);
  const target = localTarget(url.pathname, join(dist, 'llms.txt'));
  if (!target || !existsSync(target)) failures.push(`llms.txt: broken internal link ${match[1]}`);
}

const llmsFull = readFileSync(join(dist, 'llms-full.txt'), 'utf8');
// 600 KB since 2026-08-12, raised from 500 KB. The original is called an
// "initial ceiling" by the spec that set it (docs/inbox/
// public-site-discovery-performance-spec.md §6.3) — a self-imposed bound to
// keep the bundle from growing unwatched, never an external limit any consumer
// imposes. P13 added the documentation for a release's worth of capabilities
// and the corpus landed at 502,767 bytes: 0.55% over, and every byte of the
// overage is prose an agent needs. Raising a bound whose whole purpose is to
// make growth VISIBLE is the correct outcome of it having done its job, as
// long as the new number is deliberate: 600 KB is ~20% headroom on today's
// corpus. What would NOT be correct is raising it again without reading this.
//
// 700 KB since 2026-08-12, and this is the SECOND raise in one day — which is
// the thing to notice, not the number. v0.19.10 published 49 release notes,
// six times v0.19.9's eight, and the corpus landed at 607 KB.
//
// This file is a deliberately COMPLETE text projection: docs plus every
// published release. It grows monotonically and forever, so a fixed byte cap
// on it is a TRIPWIRE against accidental duplication, never a budget on the
// content. Read it that way when it fires: the question is not "trim the
// corpus", it is "did something get embedded twice".
//
// It was asked that question here, and the answer was no — the overage is 49
// genuine release notes. The same audit found 24 KiB gzip of a DIFFERENT
// payload shipped into every page of the site (the newest release's change
// list, which no consumer reads) and 32 KiB more inside the demo. Those were
// deleted, not budgeted for. That is what this tripwire is for, and it worked.
if (Buffer.byteLength(llmsFull) > 700_000) failures.push('llms-full.txt: raw corpus exceeds 700 KB');
for (const marker of ['## Bundle provenance', 'Source revision:', 'Source snapshot timestamp:', '## Table of contents', 'Roadmap — non-committed work is labelled']) {
  if (!llmsFull.includes(marker)) failures.push(`llms-full.txt: missing provenance marker ${marker}`);
}

if (failures.length) {
  console.error(`dist check failed (${failures.length})`);
  failures.forEach((failure) => console.error(`- ${failure}`));
  process.exit(1);
}

console.log(`dist check passed: ${filesBelow(dist).length} files, ${manifest.routes.length} route contracts, discovery metadata, agent indexes and gzip budgets`);
