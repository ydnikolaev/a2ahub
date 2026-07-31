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
const guideStyles = `.ssot-doc-body{color:var(--body);font-size:16px;line-height:1.7}.ssot-doc-body>:first-child{margin-top:0}.ssot-doc-body>:last-child{margin-bottom:0}.ssot-doc-body h2,.ssot-doc-body h3,.ssot-doc-body h4{color:var(--text);line-height:1.2;letter-spacing:-.02em}.ssot-doc-body h2{margin:32px 0 12px;padding-top:26px;border-top:1px solid var(--hairline);font-size:25px}.ssot-doc-body h2:first-child{margin-top:0;padding-top:0;border-top:0}.ssot-doc-body h3{margin:25px 0 9px;font-size:20px}.ssot-doc-body p{margin:0 0 15px}.ssot-doc-body ul,.ssot-doc-body ol{padding-left:22px}.ssot-doc-body pre{max-width:100%;overflow:auto;padding:14px 16px;border-radius:10px;background:var(--inverse);color:var(--on-inverse-2);font:14px/1.65 "IBM Plex Mono",monospace}.ssot-doc-body code:not(pre code){padding:2px 5px;border-radius:5px;background:var(--sink);font:.9em/1.5 "IBM Plex Mono",monospace}.ssot-doc-body table{width:100%;display:block;overflow-x:auto;margin:16px 0 22px;border:1px solid var(--border);border-radius:10px;border-collapse:separate;border-spacing:0;font-size:14px}.ssot-doc-body th,.ssot-doc-body td{min-width:120px;padding:10px 12px;border-bottom:1px solid var(--border);text-align:left;vertical-align:top}.ssot-doc-body tr:last-child td{border-bottom:0}`;

const runtime = read(runtimeOut).replaceAll('</script>', '<\\/script>');
const template = `<!doctype html>
<html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>a2a html — local dashboard</title>
<style>${fonts}\n/*A2A_SHARED_TOKENS*/\n*{box-sizing:border-box}body{margin:0;background:var(--canvas);color:var(--text);font-family:"Onest","Segoe UI",Helvetica,Arial,sans-serif;font-size:16px;-webkit-font-smoothing:antialiased}button,input{font:inherit}button:focus-visible,a:focus-visible{outline:2px solid var(--focus);outline-offset:2px;border-radius:4px}${guideStyles}</style></head><body>
${root}
<script type="text/x-dc" data-dc-script>${logic.replaceAll('</script>', '<\\/script>')}</script>
<script>window.A2A_DEMO=/*A2A_DATA_START*/{}/*A2A_DATA_END*/;window.A2A_DOCS=/*A2A_DOCS_START*/[]/*A2A_DOCS_END*/;window.__resources={};window.__resourceBlobs={};${resourceSetup}</script>
<script>${runtime}</script>
</body></html>`;

const cleanTemplate = template.replace(/[ \t]+$/gm, '');
writeFileSync(join(internal, 'template.html'), cleanTemplate);
rmSync(runtimeDir, { recursive: true });
console.log(`local dashboard: ${cleanTemplate.length} byte template + injected DATA/DOCS`);
