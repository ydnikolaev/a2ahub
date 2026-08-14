import { expect, test } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

const DASHBOARD = '/dashboard.html';
const DESKTOP = { width: 1280, height: 900 };
const MOBILE = { width: 390, height: 844 };

async function openDashboard(page, { theme, locale, viewport }) {
  await page.setViewportSize(viewport);
  await page.addInitScript(({ selectedTheme, selectedLocale }) => {
    localStorage.setItem('a2a-theme', selectedTheme);
    localStorage.setItem('a2a-locale', selectedLocale);
  }, { selectedTheme: theme, selectedLocale: locale });
  await page.goto(DASHBOARD, { waitUntil: 'networkidle' });
  await expect(page.locator('[data-screen-label="Overview"]')).toBeVisible();
  await expect(page.locator('html')).toHaveAttribute('data-theme', theme);
  await expect(page.locator('html')).toHaveAttribute('lang', locale);
}

async function mutateDashboardDemo(page, mutate) {
  await page.route('**/dashboard.html', async route => {
    const response = await route.fetch();
    const source = await response.text();
    const marker = /(<script type="application\/json" data-design-runtime-payload>)([\s\S]*?)(<\/script>)/;
    const match = source.match(marker);
    expect(match, 'dashboard runtime payload').not.toBeNull();
    const payload = JSON.parse(match[2]);
    mutate(payload.demo);
    const body = source.replace(marker, `$1${JSON.stringify(payload).replaceAll('<', '\\u003c')}$3`);
    await route.fulfill({ response, body });
  });
}

async function overflowReport(page) {
  return page.evaluate(() => {
    const geometry = (element) => {
      if (!element) return null;
      const rect = element.getBoundingClientRect();
      const style = getComputedStyle(element);
      return {
        tag: element.tagName.toLowerCase(),
        text: element.textContent?.trim().replace(/\s+/g, ' ').slice(0, 100),
        left: Math.round(rect.left),
        right: Math.round(rect.right),
        width: Math.round(rect.width),
        computedWidth: style.width,
        flex: style.flex,
        flexBasis: style.flexBasis,
        margin: style.margin,
        justifyContent: style.justifyContent,
      };
    };
    const nav = document.querySelector('.a2a-topnav');
    const more = nav?.querySelector(':scope > span');
    return {
      documentWidth: document.documentElement.scrollWidth,
      viewportWidth: document.documentElement.clientWidth,
      nav: {
        shell: geometry(nav),
        firstButton: geometry(nav?.querySelector('button')),
        more: geometry(more),
      },
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
    };
  });
}

const displayMatrix = [
  ['desktop light EN', DESKTOP, 'light', 'en'],
  ['desktop light RU', DESKTOP, 'light', 'ru'],
  ['desktop dark EN', DESKTOP, 'dark', 'en'],
  ['desktop dark RU', DESKTOP, 'dark', 'ru'],
  ['mobile light EN', MOBILE, 'light', 'en'],
  ['mobile light RU', MOBILE, 'light', 'ru'],
  ['mobile dark EN', MOBILE, 'dark', 'en'],
  ['mobile dark RU', MOBILE, 'dark', 'ru'],
];

for (const [name, viewport, theme, locale] of displayMatrix) {
  test(`dashboard reflows without horizontal overflow: ${name}`, async ({ page }) => {
    await openDashboard(page, { theme, locale, viewport });
    const report = await overflowReport(page);
    expect(report.documentWidth, JSON.stringify(report, null, 2)).toBeLessThanOrEqual(report.viewportWidth + 1);
  });
}

test('dashboard card titles keep the deliberate detail > normal > teaser hierarchy', async ({ page }) => {
  await openDashboard(page, { theme: 'light', locale: 'en', viewport: DESKTOP });

  const mediumTitle = page.locator('[data-operational-process="true"] [style*="--font-card-title-md"]').first();
  await expect(mediumTitle).toBeVisible();
  const mediumPx = await mediumTitle.evaluate((element) => Number.parseFloat(getComputedStyle(element).fontSize));

  // A top-level list card is a top-level list card wherever it appears: the
  // overview's attention rows carry the SAME medium token as the ones in
  // Threads, Contracts and Exchange. They used to be small, which is what made
  // the overview read as a different, lesser page — v0.19.9 unified them, and
  // this line is what keeps them unified.
  const attentionTitle = page.locator('[data-overview-attention="true"] [style*="--font-card-title-md"]').first();
  await expect(attentionTitle).toBeVisible();
  expect(await attentionTitle.evaluate((element) => Number.parseFloat(getComputedStyle(element).fontSize)))
    .toBe(mediumPx);

  // The small token did not disappear in that unification — it moved to the
  // tier it is actually for: a card NESTED inside another card. A thread's
  // pending-document rows are that tier, so sample it where it now lives.
  await page.getByRole('button', { name: 'Threads', exact: true }).click();
  const smallTitle = page.locator('[data-screen-label="Threads"] [style*="--font-card-title-sm"]').first();
  await expect(smallTitle).toBeVisible();
  const smallPx = await smallTitle.evaluate((element) => Number.parseFloat(getComputedStyle(element).fontSize));

  await page.getByRole('button', { name: 'Exchange', exact: true }).click();
  const detailTitle = page.locator('.a2a-detail-title').first();
  await expect(detailTitle).toBeVisible();
  const detailPx = await detailTitle.evaluate((element) => Number.parseFloat(getComputedStyle(element).fontSize));

  const tokenPx = await page.locator('html').evaluate((element) => {
    const style = getComputedStyle(element);
    return {
      detail: Number.parseFloat(style.getPropertyValue('--font-detail-title')),
      medium: Number.parseFloat(style.getPropertyValue('--font-card-title-md')),
      small: Number.parseFloat(style.getPropertyValue('--font-card-title-sm')),
    };
  });
  expect({ detailPx, mediumPx, smallPx }).toEqual({
    detailPx: tokenPx.detail,
    mediumPx: tokenPx.medium,
    smallPx: tokenPx.small,
  });
  expect(detailPx).toBeGreaterThan(mediumPx);
  expect(mediumPx).toBeGreaterThan(smallPx);
});

test('desktop navigation measures its available width before folding views', async ({ page }) => {
  await openDashboard(page, { theme: 'light', locale: 'en', viewport: DESKTOP });

  await expect(page.getByRole('button', { name: 'Threads', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Exchange', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Guide', exact: true })).toBeVisible();
});

test('type and status badges share one small-badge geometry contract', async ({ page }) => {
  await openDashboard(page, { theme: 'dark', locale: 'ru', viewport: DESKTOP });

  const attentionCard = page.locator('[data-overview-attention="true"] button').first();
  const typeBadge = attentionCard.locator('[data-type-badge] > span').first();
  const statusBadge = attentionCard.locator('[data-status-key] > span').first();
  await expect(typeBadge).toBeVisible();
  await expect(statusBadge).toBeVisible();

  const geometry = async (locator) => locator.evaluate((element) => {
    const style = getComputedStyle(element);
    return {
      height: Math.round(element.getBoundingClientRect().height * 100) / 100,
      fontSize: style.fontSize,
      lineHeight: style.lineHeight,
      borderRadius: style.borderRadius,
      paddingTop: style.paddingTop,
      paddingRight: style.paddingRight,
      paddingBottom: style.paddingBottom,
      paddingLeft: style.paddingLeft,
    };
  });

  expect(await geometry(typeBadge)).toEqual(await geometry(statusBadge));
});

test('neutral vocabulary treatment keeps six distinct non-colour cues and all words', async ({ page }) => {
  await openDashboard(page, { theme: 'light', locale: 'en', viewport: DESKTOP });

  const report = await page.evaluate(() => {
    const resolver = globalThis.A2A_VOCABULARY_RESOLVER;
    const data = window.A2A_DEMO;
    if (!resolver || typeof resolver.lookup !== 'function') throw new Error('vocabulary resolver is unavailable');
    const byTone = new Map();
    for (const entry of data?.vocabulary?.entries || []) {
      if (!byTone.has(entry.tone)) byTone.set(entry.tone, entry);
    }
    const host = document.createElement('div');
    host.setAttribute('data-vocabulary-calm-proof', 'true');
    const unchanged = [];
    for (const entry of byTone.values()) {
      const emphatic = resolver.lookup(data, entry.family, entry.value, 'en');
      const neutral = resolver.lookup(data, entry.family, entry.value, 'en', { ALL:'neutral' });
      const rendered = document.createElement('span');
      rendered.className = neutral.toneClass;
      rendered.setAttribute('data-vocabulary-cue', neutral.cueAttribute);
      rendered.textContent = neutral.cue + ' ' + neutral.label;
      host.append(rendered);
      unchanged.push(
        neutral.label === emphatic.label &&
        neutral.explanation === emphatic.explanation &&
        neutral.cue === emphatic.cue &&
        neutral.cueAttribute === emphatic.cueAttribute
      );
    }
    document.body.append(host);
    const rendered = [...host.children];
    return {
      tones: byTone.size,
      classes: rendered.map(element => element.className),
      cues: rendered.map(element => element.getAttribute('data-vocabulary-cue')),
      unchanged,
    };
  });

  expect(report.tones).toBe(6);
  expect(new Set(report.classes)).toEqual(new Set(['tone-neutral']));
  expect(new Set(report.cues).size).toBe(6);
  expect(report.unchanged).toEqual(Array(6).fill(true));
});

for (const theme of ['light', 'dark']) {
  test(`inactive master-list cards use a contrast-safe container cue and recover on hover: ${theme}`, async ({ page }) => {
    await openDashboard(page, { theme, locale: 'en', viewport: DESKTOP });
    await page.getByRole('button', { name: 'Exchange', exact: true }).click();

    const cards = page.locator('.a2a-pick');
    await expect(cards.nth(1)).toBeVisible();
    await cards.first().click();

    const appearance = card => card.evaluate((element) => {
      const style = getComputedStyle(element);
      return { opacity: style.opacity, background: style.backgroundColor, shadow: style.boxShadow };
    });
    const selected = await appearance(cards.first());
    const inactive = await appearance(cards.nth(1));
    expect(selected.opacity).toBe('1');
    expect(inactive.opacity).toBe('1');
    expect(inactive.background).not.toBe(selected.background);
    expect(inactive.shadow).not.toBe('none');

    await cards.nth(1).hover();
    await expect.poll(async () => (await appearance(cards.nth(1))).background).toBe(selected.background);
  });
}

test('all-spaces duplicate thread ids select and open the exact space row', async ({ page }) => {
  await mutateDashboardDemo(page, demo => {
    const firstSpace = demo.spaces[0].id;
    const secondSpace = demo.spaces[1].id;
    const sourceThread = demo.threads.find(thread => thread.space === firstSpace);
    const sourceView = demo.threadViews.find(view => view.space === firstSpace && view.thread === sourceThread.id);
    const duplicateID = 'thread:release-duplicate';
    const makeThread = (space, title) => ({
      ...structuredClone(sourceThread), space, id: duplicateID,
      opener: { ...structuredClone(sourceThread.opener), title },
    });
    const makeView = (space, title) => ({
      ...structuredClone(sourceView), space, thread: duplicateID,
      opener: { ...structuredClone(sourceView.opener), title },
    });
    demo.threads = [
      makeThread(firstSpace, 'First-space duplicate'),
      makeThread(secondSpace, 'Second-space duplicate'),
    ];
    demo.threadViews = [
      makeView(firstSpace, 'First-space duplicate'),
      makeView(secondSpace, 'Second-space duplicate'),
    ];
    demo.workReports = [];
  });
  await openDashboard(page, { theme: 'light', locale: 'en', viewport: DESKTOP });
  await page.getByRole('button', { name: 'Threads', exact: true }).click();

  const first = page.locator('[data-thread-id="thread:release-duplicate"][data-thread-space="checkout-core"]');
  const second = page.locator('[data-thread-id="thread:release-duplicate"][data-thread-space="customer-ops"]');
  await expect(first).toHaveAttribute('aria-pressed', 'true');
  await expect(second).toHaveAttribute('aria-pressed', 'false');
  await second.click();

  await expect(first).toHaveAttribute('aria-pressed', 'false');
  await expect(second).toHaveAttribute('aria-pressed', 'true');
  await expect(page.getByRole('heading', { name: 'Second-space duplicate', exact: true })).toBeVisible();
  await expect(page.locator('[data-screen-label="Threads"]')).toContainText('customer-ops');
});

test('guide cards stack their labels above full-width copy and retain the feedback composition', async ({ page }) => {
  await openDashboard(page, { theme: 'light', locale: 'ru', viewport: DESKTOP });
  await page.getByRole('button', { name: 'Справка', exact: true }).click();
  await expect(page.locator('[data-screen-label="Guide"]')).toBeVisible();
  await expect(page.getByText('только чтение', { exact: true })).toHaveCount(0);

  const checkStack = async (selector) => {
    const card = page.locator(selector).first();
    await expect(card).toBeVisible();
    const children = card.locator(':scope > *');
    const [cardBox, labelBox, copyBox] = await Promise.all([
      card.boundingBox(),
      children.first().boundingBox(),
      children.nth(1).boundingBox(),
    ]);
    expect(labelBox.y).toBeLessThan(copyBox.y);
    expect(copyBox.width).toBeGreaterThan(cardBox.width * 0.75);
  };

  await checkStack('[data-guide-status-card="true"]');
  await checkStack('[data-guide-reading-card="true"]');
  await expect(page.locator('[data-guide-feedback] .sc-host').first()).toBeVisible();
});

test('public Features and the local Guide use identical lifecycle tones', async ({ page }) => {
  const collectTones = () => page.locator('[data-guide-status-card="true"] [data-status-key]').evaluateAll((elements) =>
    Object.fromEntries(elements.map((element) => {
      const pill = element.firstElementChild;
      const style = getComputedStyle(pill);
      return [element.getAttribute('data-status-key'), {
        backgroundColor: style.backgroundColor,
        color: style.color,
        boxShadow: style.boxShadow,
      }];
    }))
  );

  await openDashboard(page, { theme: 'light', locale: 'en', viewport: DESKTOP });
  await page.getByRole('button', { name: 'Guide', exact: true }).click();
  await expect(page.locator('[data-guide-status-card="true"]').first()).toBeVisible();
  const local = await collectTones();

  await page.goto('/features.html', { waitUntil: 'networkidle' });
  await expect(page.locator('[data-guide-status-card="true"]').first()).toBeVisible();
  const publicFeatures = await collectTones();

  expect(publicFeatures).toEqual(local);
});

test('contract file modal stays bounded and scrollable on a mobile viewport', async ({ page }) => {
  await openDashboard(page, { theme: 'dark', locale: 'ru', viewport: MOBILE });

  // At mobile widths primary destinations live in the existing overflow menu.
  const contracts = page.getByRole('button', { name: 'Контракты', exact: true });
  if (!(await contracts.isVisible())) {
    await page.getByRole('button', { name: 'Ещё', exact: true }).click();
  }
  await page.getByRole('button', { name: 'Контракты', exact: true }).click();
  await expect(page.locator('[data-screen-label="Contracts"]')).toBeVisible();

  await page.locator('.a2a-pick').first().click();
  await page.locator('[data-contract-version="2.2.0"]').click();
  // The version package is a tree whose folders start collapsed, so a file row
  // does not exist until its directory is expanded. Reaching the modal now
  // costs one click more than it did when the package was a flat list.
  await page.locator('[data-contract-tree-dir]').first().click();
  const file = page.locator('[data-contract-file-key]').first();
  await expect(file).toBeVisible();
  await file.click();

  const dialog = page.getByRole('dialog');
  await expect(dialog).toBeVisible();
  const modalGeometry = await dialog.evaluate((element) => {
    const rect = element.getBoundingClientRect();
    const overlay = element.parentElement;
    const overlayStyle = getComputedStyle(overlay);
    return {
      left: rect.left,
      right: rect.right,
      viewportWidth: document.documentElement.clientWidth,
      overlayPosition: overlayStyle.position,
      overlayOverflowY: overlayStyle.overflowY,
      overlayScrollHeight: overlay.scrollHeight,
      overlayClientHeight: overlay.clientHeight,
    };
  });

  expect(modalGeometry.left).toBeGreaterThanOrEqual(0);
  expect(modalGeometry.right).toBeLessThanOrEqual(modalGeometry.viewportWidth + 1);
  expect(modalGeometry.overlayPosition).toBe('fixed');
  expect(['auto', 'scroll']).toContain(modalGeometry.overlayOverflowY);
  expect(modalGeometry.overlayScrollHeight).toBeGreaterThanOrEqual(modalGeometry.overlayClientHeight);

  const report = await overflowReport(page);
  expect(report.documentWidth, JSON.stringify(report, null, 2)).toBeLessThanOrEqual(report.viewportWidth + 1);
});

test('representative Russian dark mobile dashboard is axe-clean', async ({ page }) => {
  await openDashboard(page, { theme: 'dark', locale: 'ru', viewport: MOBILE });
  const results = await new AxeBuilder({ page }).analyze();
  expect(results.violations, JSON.stringify(results.violations, null, 2)).toEqual([]);
});
