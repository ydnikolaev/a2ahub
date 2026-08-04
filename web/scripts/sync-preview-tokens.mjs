import { readFileSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const webRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const repoRoot = resolve(webRoot, '..');
const tokenSource = readFileSync(join(repoRoot, 'ui/tokens.css'), 'utf8');
const sectionEnd = tokenSource.indexOf('/* Page shell');
if (sectionEnd < 0) throw new Error('preview tokens: shared token boundary is missing');

const startMarker = '/* A2A_PREVIEW_TOKENS_START — generated from ui/tokens.css */';
const endMarker = '/* A2A_PREVIEW_TOKENS_END */';
const generated = `${startMarker}\n${tokenSource.slice(0, sectionEnd).trim()}\n${endMarker}\n`;
const files = ['12-design-system-v4.dc.html', '14-local-dashboard-v4.dc.html'];
const check = process.argv.includes('--check');

for (const file of files) {
  const path = join(webRoot, 'design-source', file);
  const source = readFileSync(path, 'utf8');
  let from;
  let to;
  const marked = source.indexOf(startMarker);
  if (marked >= 0) {
    from = marked;
    const end = source.indexOf(endMarker, marked);
    if (end < 0) throw new Error(`preview tokens: end marker missing in ${file}`);
    to = end + endMarker.length + (source[end + endMarker.length] === '\n' ? 1 : 0);
  } else {
    const style = source.indexOf('<style>', source.indexOf('<helmet>'));
    from = source.indexOf(':root {', style);
    to = source.indexOf('html[data-theme="dark"] header', from);
    if (style < 0 || from < 0 || to < 0) throw new Error(`preview tokens: legacy bounds missing in ${file}`);
  }
  const next = source.slice(0, from) + generated + source.slice(to);
  if (check && next !== source) throw new Error(`preview tokens: ${file} drifted; run npm run sync:preview-tokens`);
  if (!check && next !== source) writeFileSync(path, next);
}

console.log(`preview tokens: ${check ? 'parity ok' : 'synchronized'} (${files.length} sources)`);
