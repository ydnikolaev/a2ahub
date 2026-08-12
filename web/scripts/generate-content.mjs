import { execFileSync } from 'node:child_process';
import { copyFileSync, mkdirSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { dirname, join, posix, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { marked } from 'marked';
import YAML from 'yaml';
import { publishedReleases } from './release-index.mjs';

const webRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const repoRoot = resolve(webRoot, '..');
const generated = join(webRoot, 'src/generated');
const publicRoot = join(webRoot, 'public');

mkdirSync(generated, { recursive: true });
mkdirSync(join(publicRoot, 'docs'), { recursive: true });
mkdirSync(join(publicRoot, 'setup'), { recursive: true });

const read = (path) => readFileSync(path, 'utf8');
function designArray(file, name) {
  const source = read(join(webRoot, 'design-source', file));
  const match = source.match(new RegExp(`const ${name} = (\\[[\\s\\S]*?\\n\\]);`));
  if (!match) throw new Error(`missing design-source array ${name} in ${file}`);
  return JSON.parse(match[1]);
}

// The .md routes are a projection of the SAME design source the .html page
// renders, and prose that is retyped here instead of read from there forks
// silently. It did: this generator hard-coded the roadmap's proposal as
// "verified data transfer between agents and systems … no release target"
// while the design source had long since moved that capability into SHIPPED
// ("Delivery and verdict close the data loop") and renamed the proposal to the
// autonomous loop. roadmap.md therefore told every agent reading the text
// projection that a released capability had no release target, on the same
// page whose Shipped list said it had — and roadmap.html, reading the source,
// was right the whole time.
//
// designProse throws on a stale needle for the same reason build-local-dashboard.mjs's
// `must` does: String.match returning null must be a build failure, never a
// quiet fallback to yesterday's copy.
function designProse(file, section, tag, what) {
  const source = read(join(webRoot, 'design-source', file));
  const start = source.indexOf(section);
  if (start < 0) throw new Error(`missing design-source section ${section} in ${file}`);
  const match = source.slice(start).match(new RegExp(`<${tag}[^>]*>([\\s\\S]*?)</${tag}>`));
  if (!match) throw new Error(`missing <${tag}> for ${what} after ${section} in ${file}`);
  return match[1].replace(/<[^>]+>/g, '').replace(/\s+/g, ' ').trim();
}

const manifest = JSON.parse(read(join(repoRoot, 'skill/a2ahub/docs-manifest.json')));
const site = JSON.parse(read(join(webRoot, 'content/site.json')));
const routeConfig = JSON.parse(read(join(webRoot, 'content/routes.json')));
const canonical = site.product.canonical;
const sourceRepository = site.product.github;
const releaseRepository = `${sourceRepository}/releases`;

const xmlEscape = (value) => String(value)
  .replaceAll('&', '&amp;')
  .replaceAll('<', '&lt;')
  .replaceAll('>', '&gt;')
  .replaceAll('"', '&quot;')
  .replaceAll("'", '&apos;');

function gitValue(args) {
  try {
    return execFileSync('git', args, { cwd: repoRoot, encoding: 'utf8' }).trim() || null;
  } catch {
    return null;
  }
}

const sourceRevision = gitValue(['rev-parse', '--short=12', 'HEAD']) ?? 'unavailable';
const sourceSnapshotAt = gitValue(['show', '-s', '--format=%cI', 'HEAD']) ?? 'unavailable';
const sourceLastModified = (paths) => gitValue(['log', '-1', '--format=%cs', '--', ...paths]);

function withoutFrontmatter(source) {
  if (!source.startsWith('---\n')) return source;
  const end = source.indexOf('\n---\n', 4);
  return end === -1 ? source : source.slice(end + 5);
}

function withoutLeadingH1(source) {
  return source.replace(/^\s*# [^\n]+\n+/, '');
}

const idByPath = new Map(manifest.sections.map((entry) => [entry.file, entry.id]));
function rewriteDocLinks(source, entry, extension) {
  return source.replace(/\]\(([^)#]+)(#[^)]+)?\)/g, (all, target, hash = '') => {
    if (/^(?:[a-z]+:|\/)/i.test(target)) return all;
    const resolved = posix.normalize(posix.join(posix.dirname(entry.file), target));
    const id = idByPath.get(resolved)
      ?? manifest.sections.find((candidate) => candidate.file.startsWith(`${resolved.replace(/\/$/, '')}/`))?.id;
    return id ? `](/docs/${id}.${extension}${hash})` : all;
  });
}

const docs = manifest.sections.map((entry) => {
  const source = read(join(repoRoot, 'skill', entry.file));
  const body = rewriteDocLinks(withoutLeadingH1(withoutFrontmatter(source)), entry, 'html');
  const markdown = rewriteDocLinks(source, entry, 'md');
  const html = marked.parse(body, { gfm: true })
    .replaceAll('<pre>', '<pre tabindex="0" aria-label="Scrollable code example">')
    .replaceAll('<table>', '<table tabindex="0" aria-label="Scrollable data table">');
  return { ...entry, source, markdown, html };
});

const releases = publishedReleases(repoRoot);
const guideFeatures = designArray('14-local-dashboard-v4.dc.html', 'GUIDE_FEATURES_EN');
const roadmapShipped = designArray('19-roadmap-v4.dc.html', 'SHIPPED');
const roadmapGates = designArray('19-roadmap-v4.dc.html', 'GATES');
const roadmapExploring = designArray('19-roadmap-v4.dc.html', 'EXPLORING');
const roadmapProposalTitle = designProse('19-roadmap-v4.dc.html', '{{ showProposal }}', 'h2', 'the proposal heading');
const roadmapProposalLede = designProse('19-roadmap-v4.dc.html', '{{ showProposal }}', 'p', 'the proposal summary');

const currentIssuesPath = join(repoRoot, 'releasenotes/current/known-issues.yaml');
let currentIssues = [];
try {
  const parsed = YAML.parse(read(currentIssuesPath));
  currentIssues = parsed.issues ?? parsed.known_issues ?? [];
} catch {
  currentIssues = [];
}

const securitySource = read(join(repoRoot, 'SECURITY.md'));
const seedExport = read(join(webRoot, 'content/a2a-seed-export.md'));
// The DERIVED demo model, not the raw fixture. testdata/demo.json states the
// facts; internal/html/demo-data.json is what DemoData() makes of them, with
// ownership, row clocks and — since 0.19.10 — outcome/terminal/state-provenance
// filled in from the domain (internal/html/demo.go). Reading the raw fixture
// here is what made the public dashboard and `a2a html --demo` disagree about
// the same artifact: the local one knew a `closed` question was terminal and
// this one did not. Deriving it a second time in JavaScript is not the
// alternative — a browser deciding what a state means is the defect 0.19.10
// removed. TestDemoPublishedCopyMatchesTheDerivedModel keeps this file honest;
// regenerate with `go test ./internal/html/ -run PublishedCopy -update-demo`.
const demo = JSON.parse(read(join(repoRoot, 'internal/html/demo-data.json')));
const publicDemo = JSON.parse(JSON.stringify(demo));
// The site's demo payload carries a BOUNDED sample of each release's changes,
// not the whole thing. It exists to show the shape of the release panel; the
// real dashboard reads the notes embedded in the reader's own binary.
//
// Unbounded, this was the single heaviest thing on the guided example: 32 KiB
// gzip of release prose against 16 KiB for the entire rest of the demo. v0.19.9
// carried 8 change entries and v0.19.10 carries 49, so the page went 39 KiB
// over its budget the moment the tag existed — and since the tag push is the
// only trigger that deploys the site on a release, that is a release the site
// never receives.
const DEMO_CHANGES_PER_RELEASE = 5;
publicDemo.releaseNotes = releases.slice(0, 3).map((release, index) => ({
  ...release,
  changes: (index === 0 ? [...release.changes, ...currentIssues] : release.changes)
    .slice(0, DEMO_CHANGES_PER_RELEASE)
}));
publicDemo.meta.releaseNotesScope = `v${publicDemo.releaseNotes.map((release) => release.version).join(', v')} expanded to ${DEMO_CHANGES_PER_RELEASE} entries each; the complete published version index is projected below`;

// The shared Design Components (PascalCase files in design-source/, as opposed
// to the numbered pages) are the registry BOTH surfaces load. Deriving it from
// the directory — and copying the files into public/design/ as a build output —
// means a new component is added in exactly one place: design-source/. The
// copies used to be committed by hand, which is a silent fork waiting to happen.
const designComponents = readdirSync(join(webRoot, 'design-source'))
  .filter((name) => /^[A-Z][A-Za-z0-9]*\.dc\.html$/.test(name))
  .sort();
mkdirSync(join(publicRoot, 'design'), { recursive: true });
for (const name of designComponents) {
  // Served verbatim to the browser, so the copy must already be production-shaped:
  // the prototype's pre-custom-domain origin is rewritten here exactly as
  // design-source.ts rewrites the pages, and check-dist.mjs enforces it.
  const source = read(join(webRoot, 'design-source', name))
    .replaceAll('https://ydnikolaev.github.io/a2ahub/', `${canonical}/`)
    .replaceAll('https://ydnikolaev.github.io/a2ahub', canonical)
    .replaceAll('ydnikolaev.github.io/a2ahub', 'a2ahub.dev');
  writeFileSync(join(publicRoot, 'design', name), source);
}

const designProjection = {
  components: designComponents,
  releaseIndex: releases.map(({ version, released, headline }) => [version, released, headline]),
  latestRelease: releases[0] ?? { version: 'unavailable', released: 'date unavailable' },
  docs: manifest.groups.map((group) => [group, manifest.sections.filter((entry) => entry.group === group).map((entry) => [
    entry.id,
    entry.title,
    `skill/${entry.file}`,
    `${entry.title} ${entry.group} ${entry.file} ${docs.find((doc) => doc.id === entry.id)?.source ?? ''}`.toLowerCase()
  ])]),
  docBodies: Object.fromEntries(docs.map((doc) => [doc.id, { title: doc.title, group: doc.group, source: `skill/${doc.file}`, html: doc.html }]))
};

const interpolate = (template, values) => Object.entries(values)
  .reduce((value, [key, replacement]) => value.replaceAll(`{${key}}`, replacement), template);
const routeManifest = {
  schema: 'a2ahub.dev/routes/v1',
  canonical,
  source_revision: sourceRevision,
  source_snapshot_at: sourceSnapshotAt,
  routes: [
    ...routeConfig.routes.map((route) => ({
      ...route,
      canonical_path: route.path,
      lastmod: sourceLastModified(route.change_sources) ?? undefined
    })),
    ...docs.map((doc) => {
      const values = { id: doc.id, title: doc.title };
      return {
        id: `docs-${doc.id}`,
        path: interpolate(routeConfig.documentation.path_template, values),
        astro_path: interpolate(routeConfig.documentation.astro_path_template, values),
        canonical_path: interpolate(routeConfig.documentation.path_template, values),
        title: interpolate(routeConfig.documentation.title_template, values),
        description: interpolate(routeConfig.documentation.description_template, values),
        indexable: true,
        markdown: interpolate(routeConfig.documentation.markdown_template, values),
        llms_section: routeConfig.documentation.llms_section,
        structured_data: routeConfig.documentation.structured_data,
        html_budget_kib: routeConfig.documentation.html_budget_kib_by_id?.[doc.id]
          ?? routeConfig.documentation.html_budget_kib,
        docs_group: doc.group,
        change_sources: [`skill/${doc.file}`],
        lastmod: sourceLastModified([`skill/${doc.file}`]) ?? undefined
      };
    })
  ]
};

writeFileSync(join(generated, 'route-manifest.json'), JSON.stringify(routeManifest, null, 2));
writeFileSync(join(generated, 'content.json'), JSON.stringify({ manifest, site, docs, releases, currentIssues, securitySource, routeManifest }, null, 2));
writeFileSync(join(generated, 'demo.json'), JSON.stringify(publicDemo));
writeFileSync(join(generated, 'design.json'), JSON.stringify(designProjection));

const routeById = new Map(routeManifest.routes.map((route) => [route.id, route]));
const routeURL = (id, surface = 'html') => {
  const route = routeById.get(id);
  const path = surface === 'markdown' ? route?.markdown : route?.canonical_path;
  if (!route || !path) throw new Error(`route ${id} has no ${surface} surface`);
  return `${canonical}${path}`;
};

const homeMD = `# ${site.home.title}\n\n${site.home.lead}\n\n${site.home.support}\n\n${site.home.sections.map((section) => `## ${section.title}\n\n${section.body}`).join('\n\n')}\n\n## Install\n\n\`\`\`sh\n${site.product.install}\n\`\`\`\n\n- [Documentation](${routeURL('documentation')})\n- [Guided dashboard example](${routeURL('dashboard-example')})\n- [GitHub](${site.product.github})\n`;
const featuresMD = `# How a2ahub fits together\n\nThis page is the public projection of the dashboard Guide. Its feature catalogue below is generated from the exact same design-source array as the local Guide.\n\n${guideFeatures.map(([eyebrow, title, body]) => `## ${title}\n\n**${eyebrow}**\n\n${body}`).join('\n\n')}\n\n[Open the full dashboard demo](${routeURL('dashboard-demo')}).\n`;
const changelogMD = `# Changelog\n\nPublished releases, newest first.\n\n${releases.map((release) => `## v${release.version} — ${release.released}\n\n${release.headline}\n\n${release.changes.map((change) => `### ${change.subject}\n\n**${change.kind} · ${change.impact}**\n\n${change.detail.trim()}${change.action?.scope && change.action.scope !== 'none' ? `\n\nAction scope: ${change.action.scope}. ${change.action.why ?? ''}` : ''}`).join('\n\n')}`).join('\n\n')}\n`;
const reliabilityMD = `# Reliability\n\nReliability is a chain of bounded evidence, not a blanket badge.\n\n- Schemas validate document shape.\n- The fold validates lifecycle and authority.\n- Reference resolution validates causal links.\n- Git and pull requests preserve the inspectable record.\n- Release verification checks the binary and published evidence.\n- Missing scope is reported as unavailable, never green.\n\n## Latest release evidence\n\n${releases[0]?.headline ?? 'No published release was available at build time.'}\n\n${releases[0]?.changes?.map((change) => `- **${change.subject}:** ${change.detail.trim()}`).join('\n') ?? ''}\n`;
const installMD = `# Install a2a\n\nThe installer resolves the current GitHub Releases \`latest\` channel and verifies the downloaded binary.\n\n\`\`\`sh\n${site.product.install}\n\`\`\`\n\nThen run:\n\n\`\`\`sh\na2a version\na2a init --system <system-id> --space <space-repo-url>\na2a connect <space-repo-url>\na2a doctor\n\`\`\`\n\nFor an agent-led setup, [download the Sporo seed](${canonical}/setup/a2a.md).\n`;
const roadmapMD = `# Roadmap\n\n## Shipped\n\n${roadmapShipped.map(([title, body]) => `### ${title}\n\n${body}`).join('\n\n')}\n\n## ${roadmapProposalTitle}\n\n${roadmapProposalLede}\n\nThis proposal remains gated; it has no release target.\n\n${roadmapGates.map(([title, body]) => `- **${title}:** ${body}`).join('\n')}\n\n## Exploring\n\n${roadmapExploring.map(([title, body]) => `### ${title}\n\n${body}`).join('\n\n')}\n`;
const dashboardMD = `# Dashboard example\n\nSynthetic demo data — no live services. This public page is a read-only projection of the same canonical fixture used by \`a2a html --demo\`.\n\n[Open the interactive dashboard](${canonical}/dashboard.html).\n`;
const dashboardExampleMD = `# How to read the a2ahub dashboard\n\nThe guided public example explains the same read-only components that a local \`a2a html\` build uses. Its data is synthetic: it demonstrates spaces, typed work, threads, contracts, versions and bounded freshness without connecting to a live service.\n\nThe dashboard is an explanation surface, not a control room. Agents continue the protocol through the CLI or MCP; people use the dashboard to understand current evidence, exceptions and history.\n\n- [Open the guided example](${canonical}/dashboard-example.html)\n- [Open the full synthetic demo](${canonical}/dashboard.html)\n- [Read the protocol overview](${canonical}/docs/overview.md)\n`;
const notFoundMD = `# Not found\n\nThe requested route does not exist. Continue with the [product overview](${canonical}/), [documentation](${canonical}/docs.html), or the [dashboard demo](${canonical}/dashboard.html).\n`;

const markdownPages = {
  'index.md': homeMD,
  'features.md': featuresMD,
  'changelog.md': changelogMD,
  'security.md': `# Security\n\nCanonical policy source: [GitHub](${site.product.github}/blob/main/SECURITY.md).\n\n${securitySource}`,
  'reliability.md': reliabilityMD,
  'install.md': installMD,
  'roadmap.md': roadmapMD,
  'dashboard-example.md': dashboardExampleMD,
  'dashboard.md': dashboardMD,
  '404.md': notFoundMD
};
for (const [name, body] of Object.entries(markdownPages)) writeFileSync(join(publicRoot, name), body);
for (const doc of docs) writeFileSync(join(publicRoot, 'docs', `${doc.id}.md`), doc.markdown);

writeFileSync(join(publicRoot, 'setup/a2a.md'), seedExport);
copyFileSync(join(repoRoot, 'scripts/install.sh'), join(publicRoot, 'install.sh'));
writeFileSync(join(publicRoot, 'demo-data.json'), JSON.stringify(publicDemo));

const llms = `# a2ahub

> ${site.product.definition}

${site.product.boundary}

${site.product.useWhen} The practical value is a durable identity and history for every request, requirement, contract, decision, response and handoff: schema and lifecycle validation reject invalid exchange, while Git keeps the record inspectable and rebuildable. A human establishes authority and handles exceptions instead of relaying routine context between agents.

${site.product.notFor}

## Start here

- [Product overview](${routeURL('home', 'markdown')}): Understand the coordination bottleneck, the protocol boundary and what remains under human authority.
- [Install a2a](${routeURL('install', 'markdown')}): Install the verified release directly or hand the complete setup seed to an agent.
- [Getting started](${routeURL('docs-getting-started', 'markdown')}): Connect a system and space, then complete the first safe exchange.
- [Protocol overview](${routeURL('docs-overview', 'markdown')}): Learn where identity, authority, validation and durable state live.

## Operate the protocol

- [Work loops](${routeURL('docs-work-loops', 'markdown')}): Follow the receive, act, respond and handoff loops without inventing a second workflow.
- [Threads](${routeURL('docs-threads', 'markdown')}): Keep one intent and its evidence in one ordered conversation.
- [Contract versions and sunsetting](${routeURL('docs-contract-versions', 'markdown')}): Evolve provider-consumer contracts without erasing compatibility history.
- [Command reference](${routeURL('docs-commands', 'markdown')}): Use the CLI surface whose operations are also exposed through typed MCP tools.
- [Authoring typed documents](${routeURL('docs-authoring-work_request', 'markdown')}): Start with a bounded work request; the adjacent authoring guides cover all eight artifact families.
- [Feedback](${routeURL('docs-feedback', 'markdown')}): Submit a grounded bug report or feature request through the same validated, deduplicated agent path.

## Trust and verification

- [Reliability](${routeURL('reliability', 'markdown')}): See exactly what schemas, lifecycle folds, Git and release verification prove — and what remains unavailable.
- [Security](${routeURL('security', 'markdown')}): Read the private reporting channel, supported-version policy and release-binary verification steps.
- [Changelog](${routeURL('changelog', 'markdown')}): Inspect published release facts and any action required from an installed system.
- [Source repository](${sourceRepository}): Audit the Apache-2.0 source, schemas, tests and release evidence.

## Optional

- [Guided dashboard example](${routeURL('dashboard-example', 'markdown')}): Learn how the read-only explanation surface presents synthetic protocol evidence.
- [Roadmap](${routeURL('roadmap', 'markdown')}): Distinguish shipped capabilities from proposals and exploratory work.
- [Complete agent documentation bundle](${canonical}/llms-full.txt): Read the provenance-labelled full corpus when a compact route map is not enough.
`;
writeFileSync(join(publicRoot, 'llms.txt'), llms);

const markdownAnchor = (heading) => heading
  .toLowerCase()
  .normalize('NFKD')
  .replace(/[^\p{L}\p{N}\s-]/gu, '')
  .trim()
  .replace(/\s+/g, '-');
const llmsTocHeadings = [
  'Product overview and boundaries',
  ...docs.map((doc) => doc.title),
  'Reliability',
  'Security policy',
  'Latest published releases',
  'Roadmap — non-committed work is labelled'
];
const cleanDocBody = (doc) => withoutLeadingH1(withoutFrontmatter(doc.markdown)).trim();
const fullBundle = `# a2ahub — complete agent documentation

> A provenance-labelled retrieval bundle for the open-source, Git-backed coordination protocol between autonomous software agents.

## Bundle provenance

- Canonical site: ${canonical}/
- Compact agent index: ${canonical}/llms.txt
- Source repository: ${sourceRepository}
- Source revision: ${sourceRevision}
- Source snapshot timestamp: ${sourceSnapshotAt}
- Latest published release in this bundle: ${releases[0] ? `v${releases[0].version} (${releases[0].released})` : 'unavailable'}
- Corpus rule: product boundaries first, current embedded protocol documentation next, evidence after that, and explicitly non-committed roadmap material last.

## Table of contents

${llmsTocHeadings.map((heading) => `- [${heading}](#${markdownAnchor(heading)})`).join('\n')}

## Product overview and boundaries

Source: ${routeURL('home', 'markdown')}

${site.product.definition}

${site.product.boundary}

${site.product.useWhen}

${site.product.notFor}

${withoutLeadingH1(homeMD).trim()}

${docs.map((doc) => `## ${doc.title}\n\nCanonical Markdown source: ${routeURL(`docs-${doc.id}`, 'markdown')}\nRepository source: ${sourceRepository}/blob/main/skill/${doc.file}\n\n${cleanDocBody(doc)}`).join('\n\n')}

## Reliability

Source: ${routeURL('reliability', 'markdown')}

${withoutLeadingH1(reliabilityMD).trim()}

## Security policy

Source: ${routeURL('security', 'markdown')}

${withoutLeadingH1(securitySource).trim()}

## Latest published releases

Source: ${routeURL('changelog', 'markdown')}

${withoutLeadingH1(changelogMD).trim()}

## Roadmap — non-committed work is labelled

Source: ${routeURL('roadmap', 'markdown')}

The Shipped section is released evidence. Proposal and Exploring sections are not commitments or release promises.

${withoutLeadingH1(roadmapMD).trim()}
`;
writeFileSync(join(publicRoot, 'llms-full.txt'), fullBundle);

// Only canonical, indexable HTML belongs in the search sitemap. Markdown
// alternates remain discoverable from their HTML page and the agent indexes.
const sitemapEntries = routeManifest.routes
  .filter((route) => route.indexable)
  .map((route) => `  <url>\n    <loc>${xmlEscape(`${canonical}${route.canonical_path}`)}</loc>${route.lastmod ? `\n    <lastmod>${route.lastmod}</lastmod>` : ''}\n  </url>`)
  .join('\n');
writeFileSync(join(publicRoot, 'sitemap.xml'), `<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n${sitemapEntries}\n</urlset>\n`);

const robotsAgents = ['OAI-SearchBot', 'GPTBot', 'ChatGPT-User', 'ClaudeBot', 'Claude-SearchBot', 'Claude-User'];
writeFileSync(join(publicRoot, 'robots.txt'), `${robotsAgents.map((agent) => `User-agent: ${agent}\nAllow: /`).join('\n\n')}\n\nUser-agent: *\nAllow: /\n\nSitemap: ${canonical}/sitemap.xml\n`);

const atomEntries = releases.map((release) => {
  const releaseURL = `${releaseRepository}/tag/v${release.version}`;
  return `  <entry>\n    <title>v${xmlEscape(release.version)} — ${xmlEscape(release.headline)}</title>\n    <id>${xmlEscape(releaseURL)}</id>\n    <link href="${xmlEscape(releaseURL)}" />\n    <updated>${release.released}T00:00:00Z</updated>\n    <summary type="text">${xmlEscape(release.headline)}</summary>\n  </entry>`;
}).join('\n');
writeFileSync(join(publicRoot, 'feed.xml'), `<?xml version="1.0" encoding="UTF-8"?>\n<feed xmlns="http://www.w3.org/2005/Atom">\n  <title>a2ahub releases</title>\n  <id>${routeURL('changelog')}</id>\n  <link href="${canonical}/feed.xml" rel="self" />\n  <link href="${routeURL('changelog')}" />\n  <updated>${releases[0]?.released ?? '2026-08-01'}T00:00:00Z</updated>\n  <author><name>${xmlEscape(site.organization.name)}</name></author>\n${atomEntries}\n</feed>\n`);

writeFileSync(join(publicRoot, 'site.webmanifest'), JSON.stringify({
  name: 'a2ahub — typed coordination for autonomous agents',
  short_name: 'a2ahub',
  description: site.product.definition,
  id: '/',
  start_url: '/',
  scope: '/',
  display: 'browser',
  background_color: '#f1eee6',
  theme_color: '#f1eee6',
  icons: [
    { src: '/favicon.svg', sizes: 'any', type: 'image/svg+xml', purpose: 'any' },
    { src: '/apple-touch-icon.png', sizes: '180x180', type: 'image/png', purpose: 'any' }
  ]
}, null, 2));

mkdirSync(join(publicRoot, '.well-known'), { recursive: true });
const securityBase = new Date(`${releases[0]?.released ?? '2026-08-01'}T00:00:00Z`);
securityBase.setUTCFullYear(securityBase.getUTCFullYear() + 1);
const securityExpiry = securityBase.toISOString().replace('.000', '');
writeFileSync(join(publicRoot, '.well-known/security.txt'), `Contact: ${site.product.securityContact}\nExpires: ${securityExpiry}\nPreferred-Languages: en\nCanonical: ${canonical}/.well-known/security.txt\nPolicy: ${routeURL('security')}\n`);

console.log(`content: ${docs.length} docs, ${releases.length} tagged releases, ${Object.keys(markdownPages).length} Markdown pages`);
