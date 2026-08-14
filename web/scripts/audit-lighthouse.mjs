import { spawn } from 'node:child_process';
import { createServer } from 'node:net';
import process from 'node:process';
import lighthouse from 'lighthouse';
import * as chromeLauncher from 'chrome-launcher';
import { chromium } from '@playwright/test';

const host = '127.0.0.1';
const port = await new Promise((resolve, reject) => {
  const reservation = createServer();
  reservation.once('error', reject);
  reservation.listen(0, host, () => {
    const address = reservation.address();
    reservation.close((error) => {
      if (error) reject(error);
      else resolve(address.port);
    });
  });
});
const origin = `http://${host}:${port}`;
const routes = process.env.A2A_LIGHTHOUSE_ROUTES?.split(',').filter(Boolean)
  ?? [
    '/',
    '/features.html',
    '/docs.html',
    '/docs/overview.html',
    '/install.html',
    '/security.html',
    '/reliability.html',
    '/changelog.html',
    '/roadmap.html',
  ];
const runsPerRoute = Number.parseInt(process.env.A2A_LIGHTHOUSE_RUNS ?? '3', 10);
const thresholds = {
  performance: 0.80,
  accessibility: 0.95,
  'best-practices': 0.90,
  seo: 0.95,
};
const median = (values) => values.slice().sort((left, right) => left - right)[Math.floor(values.length / 2)];

const server = spawn('npm', ['run', 'preview', '--', '--host', host, '--port', String(port)], {
  cwd: new URL('..', import.meta.url),
  stdio: ['ignore', 'pipe', 'pipe'],
});

let serverOutput = '';
server.stdout.on('data', (chunk) => { serverOutput += chunk; });
server.stderr.on('data', (chunk) => { serverOutput += chunk; });

async function waitForServer() {
  for (let attempt = 0; attempt < 80; attempt += 1) {
    if (server.exitCode !== null) throw new Error(`Astro preview exited before it became ready:\n${serverOutput}`);
    try {
      const response = await fetch(origin);
      if (response.ok) return;
    } catch {}
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  throw new Error(`Astro preview did not start:\n${serverOutput}`);
}

let chrome;
let failed = false;
try {
  await waitForServer();
  for (const route of routes) {
    const samples = Object.fromEntries(Object.keys(thresholds).map((category) => [category, []]));
    const clsSamples = [];
    for (let run = 0; run < runsPerRoute; run += 1) {
      chrome = await chromeLauncher.launch({
        // CI installs Playwright's pinned Chromium. Local quality runs use
        // chrome-launcher's canonical system-browser discovery, matching the
        // Playwright config without requiring a duplicate browser download.
        chromePath: process.env.CI ? chromium.executablePath() : chromeLauncher.getChromePath(),
        chromeFlags: ['--headless=new', '--no-sandbox', '--disable-gpu'],
      });
      let result;
      try {
        result = await lighthouse(`${origin}${route}`, {
          port: chrome.port,
          output: 'json',
          logLevel: 'error',
          onlyCategories: Object.keys(thresholds),
        });
      } finally {
        await chrome.kill();
        chrome = undefined;
      }
      if (!result?.lhr) throw new Error(`Lighthouse returned no report for ${route} run ${run + 1}`);
      const performanceScore = result.lhr.categories.performance?.score ?? 0;
      if (performanceScore < thresholds.performance) {
        const metrics = ['first-contentful-paint', 'largest-contentful-paint', 'total-blocking-time', 'cumulative-layout-shift', 'speed-index']
          .map((id) => `${id}=${result.lhr.audits[id]?.displayValue ?? 'n/a'}`)
          .join(', ');
        console.warn(`${route} run ${run + 1} performance ${Math.round(performanceScore * 100)}: ${metrics}`);
        const layoutAuditIds = Object.keys(result.lhr.audits).filter((id) => id.includes('layout'));
        console.warn(`layout audits: ${layoutAuditIds.join(', ')}`);
        for (const id of layoutAuditIds) {
          const audit = result.lhr.audits[id];
          if (audit?.details) console.warn(`${id}: ${JSON.stringify(audit.details).slice(0, 3000)}`);
        }
      }
      for (const category of Object.keys(thresholds)) samples[category].push(result.lhr.categories[category]?.score ?? 0);
      clsSamples.push(result.lhr.audits['cumulative-layout-shift']?.numericValue ?? Number.POSITIVE_INFINITY);
    }
    const summary = Object.fromEntries(Object.entries(thresholds).map(([category, minimum]) => {
      const score = median(samples[category]);
      if (score < minimum) failed = true;
      const observed = samples[category].map((sample) => Math.round(sample * 100)).join('/');
      return [category, `${Math.round(score * 100)} median [${observed}] (min ${Math.round(minimum * 100)})`];
    }));
    const cls = median(clsSamples);
    if (cls > 0.1) failed = true;
    const clsObserved = clsSamples.map((sample) => Number.isFinite(sample) ? sample.toFixed(3) : 'missing').join('/');
    console.log(`${route}: ${Object.entries(summary).map(([name, score]) => `${name} ${score}`).join(' · ')} · CLS ${cls.toFixed(3)} median [${clsObserved}] (max 0.100)`);
  }
} finally {
  if (chrome) await chrome.kill();
  server.kill('SIGTERM');
}

if (failed) process.exit(1);
