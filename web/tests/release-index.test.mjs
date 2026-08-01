import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { publishedReleases } from '../scripts/release-index.mjs';

const git = (root, ...args) => execFileSync('git', args, { cwd: root, encoding: 'utf8' });

function fixture() {
  const root = mkdtempSync(join(tmpdir(), 'a2a-release-index-'));
  mkdirSync(join(root, 'releasenotes'));
  git(root, 'init', '-q', '-b', 'main');
  git(root, 'config', 'user.name', 'release index test');
  git(root, 'config', 'user.email', 'release-index@example.invalid');
  for (const version of ['1.0.0', '1.1.0']) {
    writeFileSync(
      join(root, 'releasenotes', `${version}.yaml`),
      `schema: release-notes/v1\nversion: "${version}"\nreleased: "2026-08-01"\nheadline: "${version}"\nchanges: []\n`,
    );
  }
  git(root, 'add', '--', 'releasenotes');
  git(root, 'commit', '-q', '-m', 'docs: author release notes');
  return root;
}

test('authored notes stay unpublished until the matching immutable tag exists', (t) => {
  const root = fixture();
  t.after(() => rmSync(root, { recursive: true, force: true }));
  assert.deepEqual(publishedReleases(root), []);

  git(root, 'tag', 'v1.0.0');
  assert.deepEqual(publishedReleases(root).map(({ version }) => version), ['1.0.0']);

  git(root, 'tag', 'v1.1.0');
  assert.deepEqual(publishedReleases(root).map(({ version }) => version), ['1.1.0', '1.0.0']);
});

test('an unreadable publication source fails closed', (t) => {
  const root = mkdtempSync(join(tmpdir(), 'a2a-release-index-no-git-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  mkdirSync(join(root, 'releasenotes'));
  writeFileSync(
    join(root, 'releasenotes', '9.9.9.yaml'),
    'schema: release-notes/v1\nversion: "9.9.9"\nreleased: "2026-08-01"\nheadline: candidate\nchanges: []\n',
  );
  assert.deepEqual(publishedReleases(root), []);
});
