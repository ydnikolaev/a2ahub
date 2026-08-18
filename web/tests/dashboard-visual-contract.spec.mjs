import { expect, test } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { execFileSync } from 'node:child_process';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const DASHBOARD = '/dashboard.html';
const DESKTOP = { width: 1280, height: 900 };
const WIDE = { width: 1440, height: 900 };
const COMPACT_DESKTOP = { width: 1024, height: 900 };
const TABLET = { width: 768, height: 900 };
const MOBILE = { width: 390, height: 844 };
const REPO_ROOT = fileURLToPath(new URL('../..', import.meta.url));
const INITIAL_ETAG = `W/"sha256:${'1'.repeat(64)}"`;
const NEXT_DIGEST = `sha256:${'2'.repeat(64)}`;
const NEXT_REVISION = `sha256:${'3'.repeat(64)}`;
const CARD_MANIFEST = JSON.parse(readFileSync(new URL('../design-source/cards.manifest.json', import.meta.url), 'utf8'));
const CARD_GOLDEN_DIR = fileURLToPath(new URL('./testdata/dashboard-card-content/', import.meta.url));
let emittedDir;
let emittedHTML;
let emittedSource;

function ensureCLIEmittedDashboard() {
  if (emittedSource) return;
  emittedDir = mkdtempSync(join(tmpdir(), 'a2a-p15-emitted-'));
  const configuredBinary = process.env.A2A_VERIFY_BINARY;
  const binary = configuredBinary && existsSync(configuredBinary) ? configuredBinary : join(emittedDir, 'a2a');
  emittedHTML = join(emittedDir, 'dashboard.html');
  if (binary !== configuredBinary) {
    execFileSync('go', ['build', '-buildvcs=false', '-o', binary, './cmd/a2a'], {
      cwd: REPO_ROOT,
      stdio: 'pipe',
    });
  }
  execFileSync(binary, ['html', '--demo', '--no-open', '--out', emittedHTML], {
    cwd: REPO_ROOT,
    stdio: 'pipe',
  });
  emittedSource = readFileSync(emittedHTML, 'utf8');
}

test.afterAll(() => {
  if (emittedDir) rmSync(emittedDir, { recursive: true, force: true });
});

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

async function openDashboardView(page, locale, labels) {
  const target = page.getByRole('button', { name: labels[locale], exact: true });
  if (!(await target.isVisible())) {
    await page.getByRole('button', { name: locale === 'ru' ? 'Ещё' : 'More', exact: true }).click();
  }
  await target.click();
}

async function openManifestCard(page, kind, locale) {
  const labels = {
    work: { en: 'Exchange', ru: 'Обмен' },
    threads: { en: 'Threads', ru: 'Треды' },
    contracts: { en: 'Contracts', ru: 'Контракты' },
    spaces: { en: 'Spaces', ru: 'Спейсы' },
  };

  if (kind === 'operational-process') {
    await page.getByRole('button', {
      name: new RegExp(`^${locale === 'ru' ? 'Открыть карточку процесса:' : 'Open process card:'}`),
    }).first().click();
  } else if (kind === 'item') {
    await openDashboardView(page, locale, labels.work);
    await page.getByRole('button', {
      name: new RegExp(`^${locale === 'ru' ? 'Открыть в Обмене:' : 'Open in Exchange:'}`),
    }).first().click();
  } else if (kind === 'work-report') {
    await openDashboardView(page, locale, labels.work);
    await page.getByRole('button', {
      name: new RegExp(`^${locale === 'ru' ? 'Открыть отчёт о работе:' : 'Open work report:'}`),
    }).first().click();
  } else if (kind === 'thread') {
    await openDashboardView(page, locale, labels.threads);
    await page.locator('[data-screen-label="Threads"] .a2a-pick').first().click();
  } else if (kind === 'contract' || kind === 'cver') {
    await openDashboardView(page, locale, labels.contracts);
    const row = page.locator('[data-screen-label="Contracts"] .a2a-pick').first();
    await row.getByText(/^XC-[A-Za-z0-9-]+$/).click();
    if (kind === 'cver') {
      const contract = page.locator('[data-card-kind="contract"]');
      await expect(contract).toBeVisible();
      await contract.locator('[data-card-fact="contract:versions"] button').first().click();
    }
  } else if (kind === 'space') {
    await openDashboardView(page, locale, labels.spaces);
    await page.locator('[data-screen-label="Spaces"]').getByRole('button', {
      name: locale === 'ru' ? 'Открыть спейс' : 'Open space', exact: true,
    }).first().click();
  } else {
    throw new Error(`missing real UI card path for manifest kind ${kind}`);
  }

  const card = page.locator(`[data-card-kind="${kind}"]`);
  await expect(card).toBeVisible();
  return card;
}

// factIdentifiers reads the ordered `data-card-fact="<kind>:<slug>"` markers
// under `region`, or null if `region` itself does not exist on the page
// (distinct from "exists but is empty" — P2's re-shaped golden format needs
// to say WHICH is true, per card-spec/README.md's retirement of byte-exact
// prose equality: the golden is fact identity, order and placement, not
// sentences, so "no region" and "region with no facts yet" are different
// facts about the composition and must not collapse into the same golden).
async function factIdentifiers(region) {
  if ((await region.count()) === 0) return null;
  return region.locator('[data-card-fact]').evaluateAll(
    nodes => nodes.map(node => node.getAttribute('data-card-fact')),
  );
}

function normalizeFactGolden(kind, regionName, facts) {
  const lines = facts === null
    ? [`(no ${regionName} region rendered for kind "${kind}")`]
    : facts.length
      ? facts
      : [`(${regionName} region rendered for kind "${kind}" with no data-card-fact markers)`];
  return Buffer.from(lines.join('\n') + '\n', 'utf8');
}

async function normalizedCardDOM(card) {
  return card.evaluate((element) => {
    const clone = element.cloneNode(true);
    for (const target of clone.querySelectorAll('[data-accented]')) {
      target.removeAttribute('data-accented');
      target.style.removeProperty('background');
      target.style.removeProperty('outline');
      target.style.removeProperty('outline-offset');
    }
    return clone.outerHTML.replace(/\s+/g, ' ').trim();
  });
}

async function closeCanonicalCard(page) {
  await page.keyboard.press('Escape');
  await expect(page.locator('[data-card-modal]')).toHaveCount(0);
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

function emittedDashboardDemo() {
  ensureCLIEmittedDashboard();
  const match = emittedSource.match(/(window\.A2A_DEMO=)([\s\S]*?)(;window\.A2A_DOCS=)/);
  expect(match, 'actual CLI dashboard data').not.toBeNull();
  return JSON.parse(match[2]);
}

function emittedDashboardWithDemo(data) {
  const marker = /(window\.A2A_DEMO=)([\s\S]*?)(;window\.A2A_DOCS=)/;
  expect(emittedSource.match(marker), 'actual CLI dashboard data marker').not.toBeNull();
  const encoded = JSON.stringify(data).replaceAll('<', '\\u003c');
  return emittedSource.replace(marker, `$1${encoded}$3`);
}

async function afterAnimationFrames(page, count = 2) {
  await page.evaluate(frames => new Promise(resolve => {
    const next = () => frames-- > 0 ? requestAnimationFrame(next) : resolve();
    next();
  }), count);
}

async function installNoNetworkProbe(page) {
  await page.addInitScript(() => {
    globalThis.__a2aNetworkProbe = { fetches: [], eventSources: [] };
    globalThis.fetch = (...args) => {
      globalThis.__a2aNetworkProbe.fetches.push(args.map(value => String(value)));
      return Promise.reject(new Error('unexpected dashboard fetch'));
    };
    globalThis.EventSource = class {
      constructor(url) {
        globalThis.__a2aNetworkProbe.eventSources.push(String(url));
      }
      addEventListener() {}
      removeEventListener() {}
      close() {}
    };
  });
}

async function assertInertEmittedDashboard(page, url) {
  await page.goto(url, { waitUntil: 'domcontentloaded' });
  const overview = page.locator('[data-screen-label="Overview"]');
  await expect(overview).toBeVisible();
  await page.evaluate(() => new Promise(resolve => setTimeout(resolve, 0)));
  await afterAnimationFrames(page);
  const settled = await overview.innerHTML();
  await afterAnimationFrames(page, 3);
  expect(await overview.innerHTML()).toBe(settled);
  expect(await page.evaluate(() => globalThis.__a2aNetworkProbe)).toEqual({ fetches: [], eventSources: [] });
  await expect(page.getByRole('button', { name: /Refresh dashboard|Обновить дашборд/ })).toHaveCount(0);
}

async function exerciseRefreshCard(page, { locale, gone }) {
  const initial = emittedDashboardDemo();
  // Loopback serves real assembled dashboards, not --demo's preview metadata.
  // Feed the actual CLI shell a live-shaped initial model so the browser test
  // also proves the generated guard accepts Data.Meta's empty production form.
  initial.meta = {};
  const next = structuredClone(initial);
  const target = initial.outbox.find(item => item.space === 'checkout-core' &&
    initial.artifactDetails.some(detail => detail.space === item.space && detail.id === item.id));
  expect(target, 'checkout-core outgoing fixture with canonical detail').toBeTruthy();
  const refreshedTitle = `P15 refreshed ${target.title}`;
  next.operational.revision = NEXT_REVISION;
  next.generatedAt = '2030-01-02T03:04:05Z';
  if (gone) {
    next.outbox = next.outbox.filter(item => item.space !== target.space || item.id !== target.id);
  } else {
    const summary = next.outbox.find(item => item.space === target.space && item.id === target.id);
    const detail = next.artifactDetails.find(item => item.space === target.space && item.id === target.id);
    expect(summary, 'surviving summary fixture').toBeTruthy();
    expect(detail, 'surviving detail fixture').toBeTruthy();
    summary.title = refreshedTitle;
    detail.title = refreshedTitle;
  }

  let eventRouteReady = false;
  let releaseEvent;
  const eventGate = new Promise(resolve => { releaseEvent = resolve; });
  let releaseGet;
  const getGate = new Promise(resolve => { releaseGet = resolve; });
  let getStarted;
  const getStart = new Promise(resolve => { getStarted = resolve; });
  const dashboardRequests = [];
  let eventRequests = 0;

  await page.route('**/api/v1/dashboard', async route => {
    const method = route.request().method();
    dashboardRequests.push({ method, headers: route.request().headers() });
    if (method === 'HEAD') {
      await route.fulfill({ status: 200, headers: { ETag: INITIAL_ETAG } });
      return;
    }
    expect(method).toBe('GET');
    getStarted();
    await getGate;
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      headers: { ETag: `W/"${NEXT_DIGEST}"` },
      body: JSON.stringify(next),
    });
  });
  await page.route('**/api/v1/events', async route => {
    eventRequests += 1;
    if (eventRequests > 1) {
      await route.abort();
      return;
    }
    eventRouteReady = true;
    await eventGate;
    await route.fulfill({
      status: 200,
      headers: { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache' },
      body: `id: ${NEXT_DIGEST}\nevent: revision\ndata: ${JSON.stringify({ revision: NEXT_REVISION, viewModel: NEXT_DIGEST })}\n\n`,
    });
  });
  await page.route('**/dashboard.html', route => route.fulfill({
    status: 200,
    contentType: 'text/html',
    body: emittedDashboardWithDemo(initial),
  }));

  await page.setViewportSize(DESKTOP);
  await page.addInitScript(({ selectedLocale }) => {
    localStorage.setItem('a2a-theme', 'dark');
    localStorage.setItem('a2a-locale', selectedLocale);
  }, { selectedLocale: locale });
  await page.goto(DASHBOARD, { waitUntil: 'domcontentloaded' });
  await expect(page.locator('[data-screen-label="Overview"]')).toBeVisible();
  await expect(page.getByRole('button', { name: locale === 'ru' ? 'Обновить дашборд' : 'Refresh dashboard' })).toBeVisible();
  await expect.poll(() => eventRouteReady).toBe(true);

  await page.getByRole('button', { name: locale === 'ru' ? 'Обмен' : 'Exchange', exact: true }).click();
  const work = page.locator('[data-screen-label="Work"]');
  await expect(work).toBeVisible();
  const outgoingLabel = locale === 'ru' ? 'Исходящие' : 'Outgoing';
  await work.locator('[data-segmented-filter="true"] button').filter({ hasText: outgoingLabel }).click();
  const spaceSwitcher = page.getByRole('button', { name: locale === 'ru' ? 'Выбрать спейс' : 'Choose a space' });
  await spaceSwitcher.click();
  await spaceSwitcher.locator('xpath=..').getByRole('button').filter({ hasText: 'checkout-core' }).click();
  await expect(spaceSwitcher).toContainText('checkout-core');

  const filter = work.locator('[data-segmented-filter="true"] button[aria-pressed="true"]');
  await expect(filter).toContainText(outgoingLabel);
  await expect(page.locator('html')).toHaveAttribute('lang', locale);
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  const workBeforeInvalidation = await work.innerHTML();
  const footerBeforeInvalidation = await page.locator('.a2a-dashboard-footer').innerText();
  const urlBeforeInvalidation = page.url();
  const navigations = [];
  page.on('framenavigated', frame => { if (frame === page.mainFrame()) navigations.push(frame.url()); });

  releaseEvent();
  const newerLabel = locale === 'ru' ? 'Доступны новые данные' : 'Newer data available';
  await expect(page.locator('.a2a-dashboard-shell-header [role="status"]')).toHaveText(new RegExp(newerLabel));
  expect(dashboardRequests.map(request => request.method)).toEqual(['HEAD']);
  expect(await work.innerHTML()).toBe(workBeforeInvalidation);
  expect(await page.locator('.a2a-dashboard-footer').innerText()).toBe(footerBeforeInvalidation);
  expect(page.url()).toBe(urlBeforeInvalidation);

  await page.getByRole('button', { name: locale === 'ru' ? 'Обновить дашборд' : 'Refresh dashboard' }).click();
  await getStart;
  await expect(page.locator('.a2a-dashboard-shell-header [role="status"]')).toHaveText(locale === 'ru' ? /Обновляется/ : /Refresh in progress/);
  expect(dashboardRequests.at(-1).headers['if-none-match']).toBe(INITIAL_ETAG);

  // Refresh is a top-shell action and the card is modal, so the truthful user
  // sequence is: start the explicit refresh, then open the card while the
  // route-controlled response is pending. The adoption must preserve that
  // in-flight selection; clicking the obscured shell through an open modal
  // would only prove a test bypass, not a reachable interaction.
  const openLabel = `${locale === 'ru' ? 'Открыть в Обмене: ' : 'Open in Exchange: '}${target.title}`;
  await page.getByRole('button', { name: openLabel, exact: true }).click();
  const cardHost = page.locator('[data-card-modal] > article');
  const cardContent = cardHost.locator('[data-card-kind="item"]');
  const closeCard = cardHost.getByRole('button', { name: locale === 'ru' ? 'Закрыть карточку' : 'Close card' });
  await expect(cardContent).toBeVisible();
  await expect(cardContent.getByRole('heading').filter({ hasText: target.title })).toBeVisible();
  await cardContent.evaluate(element => { element.__p15BrowserIdentity = 'same-card-node'; });
  const cardDisclosure = cardContent.locator('details[data-item-technical]');
  await cardDisclosure.locator('summary').click();
  await expect(cardDisclosure).toHaveAttribute('open', '');
  // Let Modal's intentional first-open focus frame finish before establishing
  // the position that hydrate must preserve; otherwise the test races that
  // initial accessibility behavior and mistakes its one-time scroll for a
  // refresh-induced shift.
  await afterAnimationFrames(page);
  await closeCard.focus();
  await expect(closeCard).toBeFocused();
  await page.evaluate(() => {
    const targetY = Math.min(240, Math.max(0, document.documentElement.scrollHeight - innerHeight));
    scrollTo(0, targetY);
  });
  await afterAnimationFrames(page);
  const scrollY = await page.evaluate(() => window.scrollY);
  expect(scrollY).toBeGreaterThan(0);
  const hash = await page.evaluate(() => location.hash);
  const cardTuple = JSON.parse(decodeURIComponent(hash.slice('#card='.length)));
  expect(cardTuple[0]).toBe('item');
  expect(cardTuple[1]).toBeTruthy();
  expect(cardTuple[2]).toBeTruthy();
  navigations.length = 0; // opening the card intentionally writes its hash

  releaseGet();
  await expect(page.locator('.a2a-dashboard-shell-header [role="status"]')).toHaveText(locale === 'ru' ? /Показана новая версия/ : /New version applied/);
  await afterAnimationFrames(page);

  expect(await cardContent.evaluate(element => element.__p15BrowserIdentity)).toBe('same-card-node');
  await expect(cardDisclosure).toHaveAttribute('open', '');
  expect(await page.evaluate(() => location.hash)).toBe(hash);
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(scrollY);
  await expect(work).toBeVisible();
  await expect(filter).toContainText(outgoingLabel);
  await expect(spaceSwitcher).toContainText('checkout-core');
  await expect(page.locator('html')).toHaveAttribute('lang', locale);
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  await expect(closeCard).toBeFocused();
  expect(navigations).toEqual([]);

  if (gone) {
    const goneText = locale === 'ru'
      ? 'Этой карточки больше нет в текущих данных. Она остаётся открытой, чтобы вы могли закончить чтение.'
      : 'This card is no longer present in the current data. It remains open so you can finish reading.';
    const banner = cardHost.locator('[data-card-gone]');
    await expect(banner).toHaveText(goneText);
    await expect(banner).toHaveAttribute('role', 'status');
    expect(await banner.evaluate(node => node.parentElement === node.closest('[role="dialog"]'))).toBe(true);
    await expect(banner).not.toHaveAttribute('tabindex');
    await expect(banner).not.toBeFocused();
    await expect(cardContent.getByRole('heading').filter({ hasText: target.title })).toBeVisible();
  } else {
    await expect(cardHost.locator('[data-card-gone]')).toHaveCount(0);
    await expect(cardContent.getByRole('heading').filter({ hasText: refreshedTitle })).toBeVisible();
  }
  expect(await page.locator('.a2a-dashboard-footer').innerText()).not.toBe(footerBeforeInvalidation);
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
  ['1440 light EN', WIDE, 'light', 'en'],
  ['1440 dark RU', WIDE, 'dark', 'ru'],
  ['1024 dark EN', COMPACT_DESKTOP, 'dark', 'en'],
  ['1024 light RU', COMPACT_DESKTOP, 'light', 'ru'],
  ['768 light EN', TABLET, 'light', 'en'],
  ['768 dark RU', TABLET, 'dark', 'ru'],
  ['mobile light EN', MOBILE, 'light', 'en'],
  ['mobile dark RU', MOBILE, 'dark', 'ru'],
];

test('card content goldens match P5 in both locales', async ({ page }) => {
  // P2 (dashboard-ui-restoration-2026-08) re-shapes this producer: the golden
  // is fact identity, order and placement — read from each region's
  // `data-card-fact` markers — not the rendered sentence. See
  // card-spec/README.md#composition-rules, "Byte-exact prose equality is
  // retired". Two regions per kind per §4.5: the card's own SUMMARY (found by
  // `[data-card-summary="<kind>"]`, ahead of the click that opens the modal)
  // and the modal's DETAIL (`[data-card-content]`, the pre-existing region).
  // P3 is the phase that gives cards their own summary region; until it
  // lands, `factIdentifiers` returns null for every kind's summary and the
  // golden names that explicitly rather than writing empty content silently.
  const rows = CARD_MANIFEST.cards;
  expect(rows).toHaveLength(7);
  expect(new Set(rows.map(row => row.kind)).size).toBe(7);
  expect(rows.every(row => /^[a-z]+(?:-[a-z]+)*$/.test(row.kind))).toBe(true);

  const update = process.env.A2A_UPDATE_CARD_GOLDENS === '1';
  if (update) mkdirSync(CARD_GOLDEN_DIR, { recursive: true });

  for (const locale of ['en', 'ru']) {
    for (const { kind } of rows) {
      await openDashboard(page, { theme: 'light', locale, viewport: DESKTOP });
      const summaryRegion = page.locator(`[data-card-summary="${kind}"]`);
      const card = await openManifestCard(page, kind, locale);
      const detailRegion = card.locator('[data-card-content]');
      await expect(detailRegion, `${kind} must expose exactly one visible P5 content region`).toHaveCount(1);
      await expect(detailRegion).toBeVisible();

      const summaryFacts = await factIdentifiers(summaryRegion);
      const detailFacts = await factIdentifiers(detailRegion);
      if (summaryFacts === null) {
        test.info().annotations.push({ type: 'card-summary-region-missing', description: kind });
      }

      const summaryActual = normalizeFactGolden(kind, 'summary', summaryFacts);
      const detailActual = normalizeFactGolden(kind, 'detail', detailFacts);
      const summaryGolden = join(CARD_GOLDEN_DIR, `${kind}.${locale}.summary.golden.txt`);
      const detailGolden = join(CARD_GOLDEN_DIR, `${kind}.${locale}.detail.golden.txt`);

      if (update) {
        writeFileSync(summaryGolden, summaryActual);
        writeFileSync(detailGolden, detailActual);
        continue;
      }

      expect(existsSync(summaryGolden), `missing card-content golden: ${summaryGolden}`).toBe(true);
      expect(summaryActual, `card-content golden differs: ${summaryGolden}`).toEqual(readFileSync(summaryGolden));
      expect(existsSync(detailGolden), `missing card-content golden: ${detailGolden}`).toBe(true);
      expect(detailActual, `card-content golden differs: ${detailGolden}`).toEqual(readFileSync(detailGolden));
    }
  }
});

test('item card DOM is identical from Overview and Exchange', async ({ page }) => {
  await openDashboard(page, { theme: 'light', locale: 'en', viewport: DESKTOP });
  const target = await page.evaluate(() => window.A2A_DEMO.aggregates.needYou.items[0]);
  await page.getByRole('button', { name: `Open card: ${target.title}`, exact: true })
    .getByText(target.title, { exact: true }).click();
  const overviewCard = page.locator('[data-card-kind="item"]');
  await expect(overviewCard).toBeVisible();
  const overviewDOM = await normalizedCardDOM(overviewCard);
  await closeCanonicalCard(page);

  await openDashboardView(page, 'en', { en: 'Exchange', ru: 'Обмен' });
  await page.getByRole('button', { name: `Open in Exchange: ${target.title}`, exact: true }).click();
  const exchangeCard = page.locator('[data-card-kind="item"]');
  await expect(exchangeCard).toBeVisible();
  expect(await normalizedCardDOM(exchangeCard)).toBe(overviewDOM);
});

test('thread card DOM is identical from operational feed and Threads', async ({ page }) => {
  await openDashboard(page, { theme: 'light', locale: 'en', viewport: DESKTOP });
  const target = await page.evaluate(() => {
    const row = window.A2A_DEMO.operational.timeline[0];
    return { space: row.space, thread: row.thread };
  });
  await page.locator('[data-operational-process="true"] [data-open-thread-card]').first().click();
  const operationalCard = page.locator('[data-card-kind="thread"]');
  await expect(operationalCard).toBeVisible();
  const operationalDOM = await normalizedCardDOM(operationalCard);
  await closeCanonicalCard(page);

  await openDashboardView(page, 'en', { en: 'Threads', ru: 'Треды' });
  await page.locator(`[data-thread-id="${target.thread}"][data-thread-space="${target.space}"]`).click();
  const threadsCard = page.locator('[data-card-kind="thread"]');
  await expect(threadsCard).toBeVisible();
  expect(await normalizedCardDOM(threadsCard)).toBe(operationalDOM);
});

test('operational-process card DOM is identical from its process and work-row entry points', async ({ page }) => {
  await openDashboard(page, { theme: 'light', locale: 'en', viewport: DESKTOP });
  const process = page.locator('[data-operational-process="true"]').first();
  await process.getByRole('button', { name:/^Open process card:/ }).click();
  const processCard = page.locator('[data-card-kind="operational-process"]');
  await expect(processCard).toBeVisible();
  const processDOM = await normalizedCardDOM(processCard);
  await closeCanonicalCard(page);

  await process.getByRole('button', { name:/^Open work row:/ }).first().click();
  const workRowCard = page.locator('[data-card-kind="operational-process"]');
  await expect(workRowCard).toBeVisible();
  expect(await normalizedCardDOM(workRowCard)).toBe(processDOM);
});

test('work-report card DOM is identical from the carried report list and linked item history', async ({ page }) => {
  await openDashboard(page, { theme: 'light', locale: 'en', viewport: DESKTOP });
  await openDashboardView(page, 'en', { en: 'Exchange', ru: 'Обмен' });
  const target = await page.evaluate(() => {
    const report = window.A2A_DEMO.workReports[0];
    const subjectID = String(report.subject_ref || '').split('@')[0];
    const item = [...window.A2A_DEMO.inbox, ...window.A2A_DEMO.outbox, ...window.A2A_DEMO.archive]
      .find(candidate => candidate.space === report.space && candidate.id === subjectID);
    return { report, item };
  });
  expect(target.item, 'the fixture must carry the linked subject item').toBeTruthy();
  await page.getByRole('button', { name:`Open work report: ${target.report.subject_ref}`, exact:true }).click();
  const listCard = page.locator('[data-card-kind="work-report"]');
  await expect(listCard).toBeVisible();
  const listDOM = await normalizedCardDOM(listCard);
  await closeCanonicalCard(page);

  await page.getByRole('button', { name:`Open in Exchange: ${target.item.title}`, exact:true }).click();
  const itemCard = page.locator('[data-card-kind="item"]');
  await expect(itemCard).toBeVisible();
  await itemCard.locator('[data-work-report-history]').getByRole('button', { name:'Open work-report card', exact:true }).click();
  const linkedCard = page.locator('[data-card-kind="work-report"]');
  await expect(linkedCard).toBeVisible();
  expect(await normalizedCardDOM(linkedCard)).toBe(listDOM);
});

test('contract-version card DOM is identical from Contracts and Map and keeps its file accent', async ({ page }) => {
  await openDashboard(page, { theme: 'light', locale: 'en', viewport: DESKTOP });
  await openDashboardView(page, 'en', { en: 'Contracts', ru: 'Контракты' });
  await page.locator('[data-screen-label="Contracts"] .a2a-pick').first().click();
  await page.getByRole('dialog').getByRole('button', { name: 'Open contract version XC-atlas-order-envelope@2.2.0', exact: true }).click();
  const contractsCard = page.locator('[data-card-kind="cver"]');
  await expect(contractsCard).toBeVisible();
  const contractsDOM = await normalizedCardDOM(contractsCard);
  await closeCanonicalCard(page);

  await openDashboardView(page, 'en', { en: 'Map', ru: 'Карта' });
  await page.getByRole('button', { name: /checkout depends on XC-atlas-order-envelope provided by atlas\./ }).first().click();
  const mapCard = page.locator('[data-card-kind="cver"]');
  await expect(mapCard).toBeVisible();
  await expect(mapCard.locator('[data-accented="true"]')).toHaveCount(1);
  expect(await normalizedCardDOM(mapCard)).toBe(contractsDOM);

  ensureCLIEmittedDashboard();
  await page.goto(pathToFileURL(emittedHTML).href, { waitUntil: 'domcontentloaded' });
  await expect(page.locator('[data-screen-label="Overview"]')).toBeVisible();
  await openDashboardView(page, 'en', { en: 'Map', ru: 'Карта' });
  await page.getByRole('button', { name: /checkout depends on XC-atlas-order-envelope provided by atlas\./ }).first().click();
  const fileCard = page.locator('[data-card-kind="cver"]');
  await expect(fileCard).toBeVisible();
  await expect(fileCard.locator('[data-accented="true"]')).toHaveCount(1);
  expect(await normalizedCardDOM(fileCard)).toBe(contractsDOM);
  const cardHash = await page.evaluate(() => window.location.hash);
  expect(cardHash).toContain('#card=');

  await page.reload({ waitUntil: 'domcontentloaded' });
  await expect(page.locator('[data-card-kind="cver"]')).toBeVisible();
  await expect(page.locator('[data-card-kind="cver"] [data-accented="true"]')).toHaveCount(1);
  expect(await page.evaluate(() => window.location.hash)).toBe(cardHash);
  expect(await normalizedCardDOM(page.locator('[data-card-kind="cver"]'))).toBe(contractsDOM);
});

test('space card DOM is identical from the space switcher and Spaces', async ({ page }) => {
  await openDashboard(page, { theme: 'light', locale: 'en', viewport: DESKTOP });
  const switcherHost = page.locator('.sc-host[data-sc-name="SpaceSwitcher"]');
  const switcher = switcherHost.getByRole('button', { name: 'Choose a space', exact: true });
  await switcher.click();
  await switcherHost.locator('button').filter({ hasText: 'checkout-core' }).click();
  await switcher.click();
  await switcherHost.getByRole('button').last().click();
  const switcherCard = page.locator('[data-card-kind="space"]');
  await expect(switcherCard).toBeVisible();
  const switcherDOM = await normalizedCardDOM(switcherCard);
  await closeCanonicalCard(page);

  await openDashboardView(page, 'en', { en: 'Spaces', ru: 'Спейсы' });
  await page.locator('[data-screen-label="Spaces"]').getByRole('button', { name: 'Open space', exact: true }).first().click();
  const spacesCard = page.locator('[data-card-kind="space"]');
  await expect(spacesCard).toBeVisible();
  expect(await normalizedCardDOM(spacesCard)).toBe(switcherDOM);
});

for (const [name, viewport, theme, locale] of displayMatrix) {
  test(`dashboard reflows without horizontal overflow: ${name}`, async ({ page }) => {
    await openDashboard(page, { theme, locale, viewport });
    const report = await overflowReport(page);
    expect(report.documentWidth, JSON.stringify(report, null, 2)).toBeLessThanOrEqual(report.viewportWidth + 1);
  });
}

for (const viewport of [WIDE, COMPACT_DESKTOP, TABLET, MOBILE]) {
  test(`every changed W3 surface holds at ${viewport.width}px`, async ({ page }) => {
    const viewLabels = [
      { en: 'Exchange', ru: 'Обмен' },
      { en: 'Threads', ru: 'Треды' },
      { en: 'Contracts', ru: 'Контракты' },
      { en: 'Map', ru: 'Карта' },
      { en: 'Spaces', ru: 'Спейсы' },
    ];
    await openDashboard(page, { theme: 'light', locale: 'en', viewport });
    for (const labels of viewLabels) {
      await openDashboardView(page, 'en', labels);
      const report = await overflowReport(page);
      expect(report.documentWidth, `${labels.en}: ${JSON.stringify(report, null, 2)}`)
        .toBeLessThanOrEqual(report.viewportWidth + 1);
    }

    for (const { kind } of CARD_MANIFEST.cards) {
      await openDashboard(page, { theme: 'light', locale: 'en', viewport });
      await openManifestCard(page, kind, 'en');
      const report = await overflowReport(page);
      expect(report.documentWidth, `${kind}: ${JSON.stringify(report, null, 2)}`)
        .toBeLessThanOrEqual(report.viewportWidth + 1);
    }
  });
}

test('dashboard card titles keep the deliberate detail > normal > teaser hierarchy', async ({ page }) => {
  await mutateDashboardDemo(page, demo => {
    const item = structuredClone(demo.inbox.find(candidate => candidate.type === 'work_request'));
    demo.aggregates.needYou = {
      ...demo.aggregates.needYou,
      items: [item],
      window: { shown: 1, total: 1, truncated: false },
    };
  });
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
  await page.locator('[data-screen-label="Work"] .a2a-pick').first().click({ position: { x: 24, y: 70 } });
  const detailTitle = page.locator('[data-card-modal] .a2a-detail-title').first();
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
  await mutateDashboardDemo(page, demo => {
    const item = structuredClone(demo.inbox.find(candidate => candidate.type === 'work_request'));
    demo.aggregates.needYou = {
      ...demo.aggregates.needYou,
      items: [item],
      window: { shown: 1, total: 1, truncated: false },
    };
  });
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
    await page.getByRole('button', { name: 'Threads', exact: true }).click();

    const cards = page.locator('[data-screen-label="Threads"] .a2a-pick');
    await expect(cards.nth(1)).toBeVisible();

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

  await expect(first).toHaveAttribute('aria-pressed', 'true');
  await expect(second).toHaveAttribute('aria-pressed', 'false');
  const dialog = page.locator('[data-card-modal] [role="dialog"]');
  await expect(dialog.locator('[data-card-kind="thread"]')).toBeVisible();
  expect(await dialog.innerText()).toContain('Second-space duplicate');
  expect(await dialog.innerText()).not.toContain('First-space duplicate');
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

test('exact contract-version card stays bounded and scrollable on a mobile viewport', async ({ page }) => {
  await openDashboard(page, { theme: 'dark', locale: 'ru', viewport: MOBILE });

  // At mobile widths primary destinations live in the existing overflow menu.
  const contracts = page.getByRole('button', { name: 'Контракты', exact: true });
  if (!(await contracts.isVisible())) {
    await page.getByRole('button', { name: 'Ещё', exact: true }).click();
  }
  await page.getByRole('button', { name: 'Контракты', exact: true }).click();
  await expect(page.locator('[data-screen-label="Contracts"]')).toBeVisible();

  await page.locator('[data-screen-label="Contracts"] .a2a-pick').first().click({ position: { x: 24, y: 68 } });
  const dialog = page.getByRole('dialog');
  await expect(dialog.locator('[data-card-kind="contract"]')).toBeVisible();
  const contractHash = await page.evaluate(() => window.location.hash);
  await dialog.locator('[data-card-fact="contract:versions"] button')
    .filter({ hasText: '2.2.0' }).click();
  await expect.poll(() => page.evaluate(() => window.location.hash)).not.toBe(contractHash);
  await expect(dialog.locator('[data-card-kind="cver"]')).toBeVisible();
  await expect(dialog.locator('[data-contract-file-key]')).toHaveCount(2);
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

for (const locale of ['en', 'ru']) {
  test(`explicit dashboard refresh preserves a surviving canonical card and all UI state: ${locale.toUpperCase()}`, async ({ page }) => {
    await exerciseRefreshCard(page, { locale, gone: false });
  });

  test(`explicit dashboard refresh retains a gone canonical card with exact copy: ${locale.toUpperCase()}`, async ({ page }) => {
    await exerciseRefreshCard(page, { locale, gone: true });
  });
}

test.describe('actual CLI-emitted dashboard', () => {
  test.beforeAll(() => {
    ensureCLIEmittedDashboard();
  });

  test('file and non-loopback HTTP render the actual a2a html output without live resources', async ({ page }) => {
    expect(emittedSource).toContain('window.A2A_DEMO=');
    await installNoNetworkProbe(page);
    await assertInertEmittedDashboard(page, pathToFileURL(emittedHTML).href);

    await page.route('http://dashboard.example.test/emitted.html', route => route.fulfill({
      status: 200,
      contentType: 'text/html',
      body: emittedSource,
    }));
    await assertInertEmittedDashboard(page, 'http://dashboard.example.test/emitted.html');
  });
});

test('representative Russian dark mobile dashboard is axe-clean', async ({ page }) => {
  await openDashboard(page, { theme: 'dark', locale: 'ru', viewport: MOBILE });
  const results = await new AxeBuilder({ page }).analyze();
  expect(results.violations, JSON.stringify(results.violations, null, 2)).toEqual([]);
});
