import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
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
const dashboard = read(join(sourceRoot, '14-local-dashboard-v4.dc.html'));
const open = dashboard.indexOf('<x-dc>');
const close = dashboard.lastIndexOf('</x-dc>');
const scriptOpen = dashboard.indexOf('<script type="text/x-dc" data-dc-script', close);
const scriptBody = dashboard.indexOf('>', scriptOpen) + 1;
const scriptClose = dashboard.indexOf('</script>', scriptBody);
if ([open, close, scriptOpen, scriptBody, scriptClose].some((n) => n < 0)) throw new Error('approved local dashboard source is incomplete');

const productionLinks = (source) => source
  .replaceAll('12-design-system-v4.dc.html', 'https://a2ahub.dev/design-system.html')
  .replaceAll('https://ydnikolaev.github.io/a2ahub/', 'https://a2ahub.dev/')
  .replaceAll('https://ydnikolaev.github.io/a2ahub', 'https://a2ahub.dev')
  .replaceAll('ydnikolaev.github.io/a2ahub', 'a2ahub.dev');
const preserveDynamicTables = (source) => source.replace(/<(\/?)(table|thead|tbody|tfoot|tr|th|td)(\b[^>]*)>/gi, '<$1sc-raw-$2$3>');
const root = preserveDynamicTables(productionLinks(dashboard.slice(open, close + '</x-dc>'.length).replace(/<helmet>[\s\S]*?<\/helmet>/, '')));
const logic = productionLinks(dashboard.slice(scriptBody, scriptClose)).replace(
  'if (!d || !d.meta || d.meta.schema !== "a2a-design-demo/v3" || d.meta.synthetic !== true)',
  'if (!d || (d.meta && d.meta.schema && (d.meta.schema !== "a2a-design-demo/v3" || d.meta.synthetic !== true)))'
);

const componentNames = ['ArtifactDetail', 'ArtifactRow', 'FactTable', 'LinkDetail', 'NetworkMap', 'StatusPill', 'TypeBadge'];
const componentEntries = componentNames.map((name) => [`./${name}.dc.html`, read(join(sourceRoot, `${name}.dc.html`))]);
const jsString = (value) => JSON.stringify(value).replaceAll('<', '\\u003c');
const resourceSetup = componentEntries.map(([url, source]) => `window.__resourceBlobs[${jsString(url)}]=new Blob([${jsString(source)}],{type:"text/html"});`).join('');

const font = (family, weight, file) => {
  const bytes = readFileSync(join(webRoot, 'node_modules', file));
  return `@font-face{font-family:${jsString(family)};font-style:normal;font-display:swap;font-weight:${weight};src:url(data:font/woff2;base64,${bytes.toString('base64')}) format("woff2")}`;
};
const fonts = [400, 500, 600, 700].map((weight) => font('Onest', weight, `@fontsource/onest/files/onest-latin-${weight}-normal.woff2`)).join('')
  + [400, 500, 600].map((weight) => font('IBM Plex Mono', weight, `@fontsource/ibm-plex-mono/files/ibm-plex-mono-latin-${weight}-normal.woff2`)).join('');

const runtime = read(runtimeOut).replaceAll('</script>', '<\\/script>');
const template = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>a2a html — local dashboard</title>
<style>${fonts}\n/*A2A_SHARED_TOKENS*/\n*{box-sizing:border-box}body{margin:0;background:var(--canvas);color:var(--text);font-family:"Onest","Segoe UI",Helvetica,Arial,sans-serif;font-size:16px;-webkit-font-smoothing:antialiased}button,input{font:inherit}button:focus-visible,a:focus-visible{outline:2px solid var(--focus);outline-offset:2px;border-radius:4px}</style></head><body>
${root}
<script type="text/x-dc" data-dc-script>${logic.replaceAll('</script>', '<\\/script>')}</script>
<script>window.A2A_DEMO=/*A2A_DATA_START*/{}/*A2A_DATA_END*/;window.A2A_DOCS=/*A2A_DOCS_START*/[]/*A2A_DOCS_END*/;window.__resources={};window.__resourceBlobs={};${resourceSetup}</script>
<script>${runtime}</script>
</body></html>`;

const cleanTemplate = template.replace(/[ \t]+$/gm, '');
writeFileSync(join(internal, 'template.html'), cleanTemplate);
rmSync(runtimeDir, { recursive: true });
console.log(`local dashboard: ${cleanTemplate.length} byte template + injected DATA/DOCS`);
