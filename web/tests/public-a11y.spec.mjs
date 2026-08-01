import { expect, test } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

const routes = [
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
const themes = ['light', 'dark'];
const summarizeViolations = (violations) => violations.map((violation) => ({
  id: violation.id,
  impact: violation.impact,
  nodes: violation.nodes.map((node) => ({
    target: node.target,
    html: node.html,
    reason: node.any?.[0]?.message || node.failureSummary
  }))
}));

for (const route of routes) {
  for (const theme of themes) {
    test(`${route} is axe-clean in ${theme} theme`, async ({ page }) => {
      await page.route(/google(?:tagmanager|-analytics)\.com/, (request) => request.abort());
      await page.addInitScript((selectedTheme) => localStorage.setItem('a2a-theme', selectedTheme), theme);
      await page.goto(route, { waitUntil: 'networkidle' });
      const results = await new AxeBuilder({ page }).analyze();
      if (results.violations.length) {
        throw new Error(JSON.stringify(summarizeViolations(results.violations), null, 2));
      }
    });
  }

  test(`${route} reflows at 390px`, async ({ page }) => {
    await page.route(/google(?:tagmanager|-analytics)\.com/, (request) => request.abort());
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto(route, { waitUntil: 'networkidle' });
    const overflow = await page.evaluate(() => ({
      documentWidth: document.documentElement.scrollWidth,
      viewportWidth: document.documentElement.clientWidth,
      offenders: [...document.querySelectorAll('body *')]
        .map((element) => {
          const rect = element.getBoundingClientRect();
          return {
            tag: element.tagName.toLowerCase(),
            className: typeof element.className === 'string' ? element.className : '',
            text: element.textContent?.trim().replace(/\s+/g, ' ').slice(0, 100),
            left: Math.round(rect.left),
            right: Math.round(rect.right),
            width: Math.round(rect.width),
          };
        })
        .filter(({ left, right, width }) => width > 0 && (left < -1 || right > document.documentElement.clientWidth + 1))
        .slice(0, 12),
    }));
    expect(overflow.documentWidth, JSON.stringify(overflow)).toBeLessThanOrEqual(overflow.viewportWidth + 1);
  });
}
