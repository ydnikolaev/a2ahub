import { execFileSync } from 'node:child_process';
import { readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import YAML from 'yaml';

const semverParts = (value) => value.split('.').map((part) => Number.parseInt(part, 10));

const compareSemver = (a, b) => {
  const aa = semverParts(a.version);
  const bb = semverParts(b.version);
  for (let i = 0; i < 3; i += 1) if (aa[i] !== bb[i]) return bb[i] - aa[i];
  return 0;
};

export function publishedReleaseTags(repoRoot) {
  try {
    const output = execFileSync(
      'git',
      ['for-each-ref', '--format=%(refname:strip=2)', 'refs/tags/v*'],
      { cwd: repoRoot, encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'] },
    );
    return new Set(output.trim().split('\n').filter(Boolean));
  } catch {
    // Publication cannot be proved without the immutable refs. Fail closed:
    // authored release notes are candidates until a matching tag is present.
    return new Set();
  }
}

export function publishedReleases(repoRoot, releaseNotesRoot = join(repoRoot, 'releasenotes')) {
  const tags = publishedReleaseTags(repoRoot);
  return readdirSync(releaseNotesRoot)
    .filter((name) => /^\d+\.\d+\.\d+\.yaml$/.test(name))
    .map((name) => YAML.parse(readFileSync(join(releaseNotesRoot, name), 'utf8')))
    .filter((release) => tags.has(`v${release.version}`))
    .sort(compareSemver);
}
