// @ts-check
import { defineConfig } from 'astro/config';

// Static output — served straight off GitHub Pages, no runtime, no third-party
// requests. `build.format: 'file'` emits /index.html at the root. `site` is the
// absolute origin stamped into canonical/OG URLs (GitHub Pages project site).
export default defineConfig({
  site: 'https://ydnikolaev.github.io',
  base: '/a2ahub',
  build: { format: 'file' },
});
