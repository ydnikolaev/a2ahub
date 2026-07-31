// @ts-check
import { defineConfig } from 'astro/config';

// Static output — served straight off GitHub Pages at the custom apex domain,
// no runtime and no third-party requests. A custom domain is rooted at `/`, so
// Astro's official Pages guidance requires no project-site `base` value.
export default defineConfig({
  site: 'https://a2ahub.dev',
  build: { format: 'file' },
});
