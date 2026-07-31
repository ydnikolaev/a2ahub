import { execFileSync } from 'node:child_process';
import { copyFileSync, mkdirSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { dirname, join, posix, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { marked } from 'marked';
import YAML from 'yaml';

const webRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const repoRoot = resolve(webRoot, '..');
const generated = join(webRoot, 'src/generated');
const publicRoot = join(webRoot, 'public');
const canonical = 'https://a2ahub.dev';

mkdirSync(generated, { recursive: true });
mkdirSync(join(publicRoot, 'docs'), { recursive: true });
mkdirSync(join(publicRoot, 'setup'), { recursive: true });

const read = (path) => readFileSync(path, 'utf8');
const manifest = JSON.parse(read(join(repoRoot, 'skill/a2ahub/docs-manifest.json')));
const site = JSON.parse(read(join(webRoot, 'content/site.json')));

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
  return { ...entry, source, markdown, html: marked.parse(body, { gfm: true }) };
});

let publishedTags = new Set();
try {
  publishedTags = new Set(execFileSync('git', ['tag', '--list', 'v*'], { cwd: repoRoot, encoding: 'utf8' }).trim().split('\n').filter(Boolean));
} catch {
  // The release-note validator and CI fetch-depth gate remain authoritative.
}

const semverParts = (value) => value.split('.').map((part) => Number.parseInt(part, 10));
const compareSemver = (a, b) => {
  const aa = semverParts(a.version);
  const bb = semverParts(b.version);
  for (let i = 0; i < 3; i += 1) if (aa[i] !== bb[i]) return bb[i] - aa[i];
  return 0;
};

const releases = readdirSync(join(repoRoot, 'releasenotes'))
  .filter((name) => /^\d+\.\d+\.\d+\.yaml$/.test(name))
  .map((name) => YAML.parse(read(join(repoRoot, 'releasenotes', name))))
  .filter((release) => publishedTags.size === 0 || publishedTags.has(`v${release.version}`))
  .sort(compareSemver);

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
const demo = JSON.parse(read(join(repoRoot, 'internal/html/testdata/demo.json')));
const publicDemo = JSON.parse(JSON.stringify(demo));
publicDemo.releaseNotes = releases.slice(0, 3).map((release, index) => ({
  ...release,
  changes: index === 0 ? [...release.changes, ...currentIssues] : release.changes
}));
publicDemo.meta.releaseNotesScope = `v${publicDemo.releaseNotes.map((release) => release.version).join(', v')} expanded; the complete published version index is projected below`;

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

writeFileSync(join(generated, 'content.json'), JSON.stringify({ manifest, site, docs, releases, currentIssues, securitySource }, null, 2));
writeFileSync(join(generated, 'demo.json'), JSON.stringify(publicDemo));
writeFileSync(join(generated, 'design.json'), JSON.stringify(designProjection));

const homeMD = `# ${site.home.title}\n\n${site.home.lead}\n\n${site.home.support}\n\n${site.home.sections.map((section) => `## ${section.title}\n\n${section.body}`).join('\n\n')}\n\n## Install\n\n\`\`\`sh\n${site.product.install}\n\`\`\`\n\n- [Documentation](${canonical}/docs.html)\n- [Dashboard example](${canonical}/dashboard.html)\n- [GitHub](${site.product.github})\n`;
const changelogMD = `# Changelog\n\nPublished releases, newest first.\n\n${releases.map((release) => `## v${release.version} — ${release.released}\n\n${release.headline}\n\n${release.changes.map((change) => `### ${change.subject}\n\n**${change.kind} · ${change.impact}**\n\n${change.detail.trim()}${change.action?.scope && change.action.scope !== 'none' ? `\n\nAction scope: ${change.action.scope}. ${change.action.why ?? ''}` : ''}`).join('\n\n')}`).join('\n\n')}\n`;
const reliabilityMD = `# Reliability\n\nReliability is a chain of bounded evidence, not a blanket badge.\n\n- Schemas validate document shape.\n- The fold validates lifecycle and authority.\n- Reference resolution validates causal links.\n- Git and pull requests preserve the inspectable record.\n- Release verification checks the binary and published evidence.\n- Missing scope is reported as unavailable, never green.\n\n## Latest release evidence\n\n${releases[0]?.headline ?? 'No published release was available at build time.'}\n\n${releases[0]?.changes?.map((change) => `- **${change.subject}:** ${change.detail.trim()}`).join('\n') ?? ''}\n`;
const installMD = `# Install a2a\n\nThe installer resolves the current GitHub Releases \`latest\` channel and verifies the downloaded binary.\n\n\`\`\`sh\n${site.product.install}\n\`\`\`\n\nThen run:\n\n\`\`\`sh\na2a version\na2a init --system <system-id> --space <space-repo-url>\na2a connect <space-repo-url>\na2a doctor\n\`\`\`\n\nFor an agent-led setup, [download the Sporo seed](${canonical}/setup/a2a.md).\n`;
const roadmapMD = `# Roadmap\n\n## Shipped\n\n${site.roadmap.shipped.map((item) => `- ${item}`).join('\n')}\n\n## Proposal — ${site.roadmap.proposal.title}\n\n${site.roadmap.proposal.body}\n\n${site.roadmap.proposal.labels.map((label) => `- ${label}`).join('\n')}\n\n## Exploring\n\n${site.roadmap.exploring.map((item) => `- ${item}`).join('\n')}\n`;
const dashboardMD = `# Dashboard example\n\nSynthetic demo data — no live services. This public page is a read-only projection of the same canonical fixture used by \`a2a html --demo\`.\n\n[Open the interactive dashboard](${canonical}/dashboard.html).\n`;
const notFoundMD = `# Not found\n\nThe requested route does not exist. Continue with the [product overview](${canonical}/), [documentation](${canonical}/docs.html), or the [dashboard demo](${canonical}/dashboard.html).\n`;

const markdownPages = {
  'index.md': homeMD,
  'changelog.md': changelogMD,
  'security.md': `# Security\n\nCanonical policy source: [GitHub](${site.product.github}/blob/main/SECURITY.md).\n\n${securitySource}`,
  'reliability.md': reliabilityMD,
  'install.md': installMD,
  'roadmap.md': roadmapMD,
  'dashboard.md': dashboardMD,
  '404.md': notFoundMD
};
for (const [name, body] of Object.entries(markdownPages)) writeFileSync(join(publicRoot, name), body);
for (const doc of docs) writeFileSync(join(publicRoot, 'docs', `${doc.id}.md`), doc.markdown);

writeFileSync(join(publicRoot, 'setup/a2a.md'), seedExport);
copyFileSync(join(repoRoot, 'scripts/install.sh'), join(publicRoot, 'install.sh'));
writeFileSync(join(publicRoot, 'demo-data.json'), JSON.stringify(publicDemo));

const routeIndex = [
  ['Home', '/index.md', site.home.lead],
  ['Documentation', '/docs.html', 'Complete agent-facing manual projected from the embedded skill.'],
  ['Dashboard example', '/dashboard.html', 'Synthetic read-only view of spaces, work, threads and contracts.'],
  ['Changelog', '/changelog.md', 'Published structured release notes.'],
  ['Security', '/security.md', 'Security policy, trust boundaries and reporting.'],
  ['Reliability', '/reliability.md', 'Bounded validation and release evidence.'],
  ['Install', '/install.md', 'Direct and agent-led latest-channel setup.'],
  ['Roadmap', '/roadmap.md', 'Shipped, proposal and exploring horizons.']
];
writeFileSync(join(publicRoot, 'llms.txt'), `# a2ahub\n\n${site.product.tagline}. Agents operate the protocol; humans establish authority and inspect exceptions.\n\n${routeIndex.map(([title, href, summary]) => `- [${title}](${canonical}${href}): ${summary}`).join('\n')}\n`);
writeFileSync(join(publicRoot, 'llms-full.txt'), `# a2ahub — complete agent documentation\n\n${homeMD}\n\n${docs.map((doc) => `# ${doc.title}\n\n${doc.source}`).join('\n\n')}\n\n${changelogMD}\n\n${securitySource}\n\n${roadmapMD}`);

// The design system is an internal reference, not a public route: it is built
// from design-source/ and read locally, never published or indexed.
const htmlRoutes = ['', 'docs.html', ...docs.map((doc) => `docs/${doc.id}.html`), 'dashboard.html', 'dashboard-example.html', 'changelog.html', 'security.html', 'reliability.html', 'install.html', 'roadmap.html'];
const mdRoutes = Object.keys(markdownPages).concat(docs.map((doc) => `docs/${doc.id}.md`), ['llms.txt', 'llms-full.txt', 'setup/a2a.md']);
const sitemap = [...htmlRoutes, ...mdRoutes].map((route) => `  <url><loc>${canonical}/${route}</loc></url>`).join('\n');
writeFileSync(join(publicRoot, 'sitemap.xml'), `<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n${sitemap}\n</urlset>\n`);
writeFileSync(join(publicRoot, 'robots.txt'), `User-agent: *\nAllow: /\nSitemap: ${canonical}/sitemap.xml\n`);

console.log(`content: ${docs.length} docs, ${releases.length} tagged releases, ${Object.keys(markdownPages).length} Markdown pages`);
