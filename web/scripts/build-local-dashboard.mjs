import { mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { build } from 'vite';

const webRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const repoRoot = resolve(webRoot, '..');
const internal = join(repoRoot, 'internal/html');
const sourceRoot = join(webRoot, 'design-source');
const runtimeDir = mkdtempSync('/private/tmp/a2a-dashboard-runtime-');
const runtimeOut = join(runtimeDir, 'design-runtime.js');

await build({
  configFile: false,
  logLevel: 'warn',
  define: {
    'process.env.NODE_ENV': JSON.stringify('production')
  },
  build: {
    emptyOutDir: false,
    lib: {
      entry: join(webRoot, 'scripts/design-runtime-entry.js'),
      formats: ['iife'],
      name: 'A2ADesignRuntime',
      fileName: () => 'design-runtime.js'
    },
    outDir: runtimeDir,
    minify: true,
    sourcemap: false,
    rollupOptions: {}
  }
});

const read = (path) => readFileSync(path, 'utf8');

// Every production rewrite below is an exact-literal substitution against the
// approved design source. String.replace with a stale needle does NOT throw: it
// returns the input unchanged and the dashboard builds green while behaving
// wrong. `must` turns a drifted needle into a loud build failure, so editing
// 14-local-dashboard-v4.dc.html can never silently strand a rewrite.
const must = (source, from, to) => {
  if (!source.includes(from)) throw new Error(`local dashboard build: needle no longer present in the design source:\n${from}`);
  return source.replaceAll(from, to);
};
const mustAll = (source, pairs) => pairs.reduce((acc, [from, to]) => must(acc, from, to), source);

const dashboard = read(join(sourceRoot, '14-local-dashboard-v4.dc.html'));
const open = dashboard.indexOf('<x-dc>');
const close = dashboard.lastIndexOf('</x-dc>');
const scriptOpen = dashboard.indexOf('<script type="text/x-dc" data-dc-script', close);
const scriptBody = dashboard.indexOf('>', scriptOpen) + 1;
const scriptClose = dashboard.indexOf('</script>', scriptBody);
if ([open, close, scriptOpen, scriptBody, scriptClose].some((n) => n < 0)) throw new Error('approved local dashboard source is incomplete');

const productionLinks = (source) => source
  .replaceAll('https://ydnikolaev.github.io/a2ahub/', 'https://a2ahub.dev/')
  .replaceAll('https://ydnikolaev.github.io/a2ahub', 'https://a2ahub.dev')
  .replaceAll('ydnikolaev.github.io/a2ahub', 'a2ahub.dev');
const preserveDynamicTables = (source) => source.replace(/<(\/?)(table|thead|tbody|tfoot|tr|th|td)(\b[^>]*)>/gi, '<$1sc-raw-$2$3>');
const root = preserveDynamicTables(productionLinks(dashboard.slice(open, close + '</x-dc>'.length).replace(/<helmet>[\s\S]*?<\/helmet>/, '')));
// The prototype hard-refuses any fixture that is not the synthetic demo; the
// shipped dashboard must accept real, non-synthetic DATA and keep refusing a
// fixture that claims the demo schema wrongly.
const logic = must(
  productionLinks(dashboard.slice(scriptBody, scriptClose)),
  'if (!d || !d.meta || d.meta.schema !== "a2a-design-demo/v3" || d.meta.synthetic !== true)',
  'if (!d || (d.meta && d.meta.schema && (d.meta.schema !== "a2a-design-demo/v3" || d.meta.synthetic !== true)))'
);

// Which components to inline is derived, not listed: walk `dc-import name="X"`
// from the dashboard page and follow it transitively (NetworkMap pulls in
// LinkDetail and StatusPill). Adding a component to the design source is then a
// one-place change, and the site-only shell components (SiteHeader/SiteFooter)
// never get carried into this single-file build as dead weight.
const importedNames = (source) => [...source.matchAll(/<dc-import\s+name="([A-Za-z0-9]+)"/g)].map((m) => m[1]);
const componentFiles = [];
const pending = importedNames(root);
const known = new Set(readdirSync(sourceRoot).filter((name) => /^[A-Z][A-Za-z0-9]*\.dc\.html$/.test(name)));
const seen = new Set();
while (pending.length) {
  const name = pending.shift();
  const file = `${name}.dc.html`;
  if (seen.has(file)) continue;
  if (!known.has(file)) throw new Error(`local dashboard build: dc-import "${name}" has no design-source component`);
  seen.add(file);
  componentFiles.push(file);
  pending.push(...importedNames(read(join(sourceRoot, file))));
}
componentFiles.sort();
const componentEntries = componentFiles.map((name) => [`./${name}`, read(join(sourceRoot, name))]);
const jsString = (value) => JSON.stringify(value).replaceAll('<', '\\u003c');
const resourceSetup = componentEntries.map(([url, source]) => `window.__resourceBlobs[${jsString(url)}]=new Blob([${jsString(source)}],{type:"text/html"});`).join('');

// Subsets, not just weights. The dashboard ships a full RU locale, and a
// latin-only embed silently fell back to the system font for every Cyrillic
// string — the interface changed typeface when you pressed RU. Each face
// declares its own unicode-range so the browser downloads nothing it does not
// need; the cost here is bytes in one self-contained file (~50KB raw), which is
// the price of the offline guarantee.
const RANGES = {
  latin: 'U+0000-00FF,U+0131,U+0152-0153,U+02BB-02BC,U+02C6,U+02DA,U+02DC,U+0304,U+0308,U+0329,U+2000-206F,U+20AC,U+2122,U+2191,U+2193,U+2212,U+2215,U+FEFF,U+FFFD',
  'latin-ext': 'U+0100-02BA,U+02BD-02C5,U+02C7-02CC,U+02CE-02D7,U+02DD-02FF,U+0304,U+0308,U+0329,U+1D00-1DBF,U+1E00-1E9F,U+1EF2-1EFF,U+2020,U+20A0-20AB,U+20AD-20C0,U+2113,U+2C60-2C7F,U+A720-A7FF',
  cyrillic: 'U+0301,U+0400-045F,U+0490-0491,U+04B0-04B1,U+2116',
  'cyrillic-ext': 'U+0460-052F,U+1C80-1C8A,U+20B4,U+2DE0-2DFF,U+A640-A69F,U+FE2E-FE2F'
};
// Every subset is paid unconditionally here — a data: URI in a single offline
// file is downloaded whether unicode-range matches or not — so the list is the
// two alphabets this interface actually ships (EN and RU), plus latin-ext for
// European names and identifiers that appear inside artifact data. cyrillic-ext
// (old Church Slavonic, ₴) costs 26KB for glyphs neither locale uses.
const SUBSETS = ['latin', 'latin-ext', 'cyrillic'];
const face = (family, weight, pkg, slug, subset) => {
  const bytes = readFileSync(join(webRoot, 'node_modules', `${pkg}/files/${slug}-${subset}-${weight}-normal.woff2`));
  return `@font-face{font-family:${jsString(family)};font-style:normal;font-display:swap;font-weight:${weight};unicode-range:${RANGES[subset]};src:url(data:font/woff2;base64,${bytes.toString('base64')}) format("woff2")}`;
};
const family = (name, pkg, slug, weights) => weights.flatMap((weight) => SUBSETS.map((subset) => face(name, weight, pkg, slug, subset))).join('');
const fonts = family('Onest', '@fontsource/onest', 'onest', [400, 500, 600, 700])
  + family('IBM Plex Mono', '@fontsource/ibm-plex-mono', 'ibm-plex-mono', [400, 500, 600]);
const guideStyles = `.ssot-doc-body{color:var(--body);font-size:16px;line-height:1.7}.ssot-doc-body>:first-child{margin-top:0}.ssot-doc-body>:last-child{margin-bottom:0}.ssot-doc-body h2,.ssot-doc-body h3,.ssot-doc-body h4{color:var(--text);line-height:1.2;letter-spacing:-.02em}.ssot-doc-body h2{margin:32px 0 12px;padding-top:26px;border-top:1px solid var(--hairline);font-size:25px}.ssot-doc-body h2:first-child{margin-top:0;padding-top:0;border-top:0}.ssot-doc-body h3{margin:25px 0 9px;font-size:20px}.ssot-doc-body p{margin:0 0 15px}.ssot-doc-body ul,.ssot-doc-body ol{padding-left:22px}.ssot-doc-body pre{max-width:100%;overflow:auto;padding:14px 16px;border-radius:10px;background:var(--inverse);color:var(--on-inverse-2);font:14px/1.65 "IBM Plex Mono",monospace}.ssot-doc-body code:not(pre code){padding:2px 5px;border-radius:5px;background:var(--sink);font:.9em/1.5 "IBM Plex Mono",monospace}.ssot-doc-body table{width:100%;display:block;overflow-x:auto;margin:16px 0 22px;border:1px solid var(--border);border-radius:10px;border-collapse:separate;border-spacing:0;font-size:14px}.ssot-doc-body th,.ssot-doc-body td{min-width:120px;padding:10px 12px;border-bottom:1px solid var(--border);text-align:left;vertical-align:top}.ssot-doc-body tr:last-child td{border-bottom:0}`;

const runtime = read(runtimeOut).replaceAll('</script>', '<\\/script>');
const template = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>a2a html — local dashboard</title>
<style>${fonts}\n/*A2A_SHARED_TOKENS*/\n*{box-sizing:border-box}body{margin:0;background:var(--canvas);color:var(--text);font-family:"Onest","Segoe UI",Helvetica,Arial,sans-serif;font-size:16px;-webkit-font-smoothing:antialiased}button,input{font:inherit}button:focus-visible,a:focus-visible{outline:2px solid var(--focus);outline-offset:2px;border-radius:4px}${guideStyles}</style></head><body>
${root}
<script type="text/x-dc" data-dc-script>${logic.replaceAll('</script>', '<\\/script>')}</script>
<script>window.A2A_DEMO=/*A2A_DATA_START*/{}/*A2A_DATA_END*/;window.A2A_DOCS=/*A2A_DOCS_START*/[]/*A2A_DOCS_END*/;window.__resources={};window.__resourceBlobs={};${resourceSetup}</script>
<script>${runtime}</script>
</body></html>`;

// Trailing newline: the committed template.html is a tracked text file, so the
// generator must produce exactly what a POSIX text file looks like — otherwise
// every regeneration shows a one-line diff and the drift gate can never be byte-exact.
const cleanTemplate = `${template.replace(/[ \t]+$/gm, '')}\n`;
writeFileSync(join(internal, 'template.html'), cleanTemplate);
rmSync(runtimeDir, { recursive: true });
console.log(`local dashboard: ${cleanTemplate.length} byte template + injected DATA/DOCS`);
