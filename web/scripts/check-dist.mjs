import { existsSync, readFileSync, readdirSync } from 'node:fs';
import { extname, join, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const webRoot = resolve(fileURLToPath(new URL('..', import.meta.url)));
const dist = join(webRoot, 'dist');
const failures = [];

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

for (const file of filesBelow(dist)) {
  const source = readFileSync(file, 'utf8');
  const name = relative(dist, file);

  const isDesignResource = name.startsWith('design/');
  if (!isDesignResource && (/\b(?:src|srcset)=["']https?:\/\//i.test(source) || /url\(\s*["']?https?:\/\//i.test(source))) {
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

for (const required of ['CNAME', 'llms.txt', 'llms-full.txt', 'sitemap.xml', 'robots.txt', 'setup/a2a.md', 'demo-data.json', 'install.sh']) {
  if (!existsSync(join(dist, required))) failures.push(`missing generated ${required}`);
}

if (readFileSync(join(dist, 'CNAME'), 'utf8').trim() !== 'a2ahub.dev') failures.push('CNAME is not a2ahub.dev');

if (failures.length) {
  console.error(`dist check failed (${failures.length})`);
  failures.forEach((failure) => console.error(`- ${failure}`));
  process.exit(1);
}

console.log(`dist check passed: ${filesBelow(dist).length} files, local links resolve, no automatic remote requests`);
