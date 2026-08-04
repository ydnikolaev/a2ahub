import { execFileSync } from 'node:child_process';
import test from 'node:test';

test('design preview token blocks are generated from the runtime token SSOT', () => {
  execFileSync(process.execPath, ['scripts/sync-preview-tokens.mjs', '--check'], {
    cwd: new URL('..', import.meta.url),
    stdio: 'pipe'
  });
});
