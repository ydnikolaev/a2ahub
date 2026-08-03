import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';
import { runtimeDesignPage } from '../src/lib/design-source.ts';

function dashboardController({ fetch, EventSource }) {
  class DCLogic {
    setState(update) {
      const patch = typeof update === 'function' ? update(this.state) : update;
      this.state = Object.assign({}, this.state, patch);
    }
  }
  const documentElement = { lang: 'en', dataset: {}, setAttribute() {} };
  const context = {
    DCLogic,
    EventSource,
    fetch,
    console,
    setTimeout,
    clearTimeout,
    URLSearchParams,
    document: {
      documentElement,
      body: { dataset: {} },
      addEventListener() {},
      removeEventListener() {},
      querySelector() { return null; }
    },
    location: { protocol: 'http:', hostname: '127.0.0.1', search: '', hash: '' },
    localStorage: { getItem() { return null; }, setItem() {} },
    navigator: { clipboard: { writeText: async () => {} } }
  };
  context.addEventListener = () => {};
  context.removeEventListener = () => {};
  context.scrollTo = () => {};
  context.window = context;
  context.globalThis = context;
  const { logic } = runtimeDesignPage('14-local-dashboard-v4.dc.html');
  vm.runInNewContext(`${logic}\nglobalThis.__DashboardComponent = Component;`, context);
  return new context.__DashboardComponent();
}

const nextTurn = () => new Promise(resolve => setImmediate(resolve));

test('the public Features projection contains only the dashboard Guide screen', () => {
  const { template, logic } = runtimeDesignPage('14-local-dashboard-v4.dc.html', 'guide');

  assert.match(template, /data-screen-label="Guide"/);
  assert.doesNotMatch(template, /aria-label="\{\{ guideAria \}\}"/);
  assert.doesNotMatch(template, /toggleTypeMenu|integrityFlagsLabel|data-screen-label="Overview"/);
  assert.match(logic, /const DASHBOARD_I18N = \{ en: DASHBOARD_EN \}/);
  assert.match(logic, /const A2A_GLOSSARY = \{ en: A2A_GLOSSARY_EN \}/);
  assert.doesNotMatch(logic, /DASHBOARD_RU|A2A_GLOSSARY_RU|GUIDE_FEATURES_RU|GUIDE_RU/);
  assert.doesNotMatch(logic, /[А-Яа-яЁё]/);
});

test('the local dashboard projection retains both supported locales', () => {
  const { logic } = runtimeDesignPage('14-local-dashboard-v4.dc.html');

  assert.match(logic, /const DASHBOARD_I18N = \{ en: DASHBOARD_EN, ru: DASHBOARD_RU \}/);
  assert.match(logic, /const A2A_GLOSSARY = \{ en: A2A_GLOSSARY_EN, ru: A2A_GLOSSARY_RU \}/);
  assert.match(logic, /GUIDE_FEATURES_RU/);
  assert.match(logic, /[А-Яа-яЁё]/);
});

test('the live dashboard applies only the newest conditional snapshot and preserves attention data', async () => {
  const gets = [];
  const pending = [];
  class FakeEventSource {
    static instance;
    constructor(url) { this.url = url; this.handlers = {}; FakeEventSource.instance = this; }
    addEventListener(name, handler) { this.handlers[name] = handler; }
    close() { this.closed = true; }
  }
  const controller = dashboardController({
    EventSource: FakeEventSource,
    fetch: (url, options = {}) => {
      if (options.method === 'HEAD') return Promise.resolve({ ok: true });
      gets.push({ url, options });
      return new Promise(resolve => pending.push(resolve));
    }
  });
  const attention = [{ id: 'XQ-attention', reasons: ['gate-pending-on-me'], prompt: { text: 'review' } }];
  controller.state.data = { inbox: attention, operational: { revision: 'sha256:old', sources: [], timeline: [], unavailable: [] } };
  controller.startOperationalStream('sha256:old');
  await nextTurn();

  const source = FakeEventSource.instance;
  assert.equal(source.url, '/api/v1/events');
  source.handlers.revision({ data: JSON.stringify({ revision: 'sha256:two' }), lastEventId: 'sha256:two' });
  source.handlers.revision({ data: JSON.stringify({ revision: 'sha256:three' }), lastEventId: 'sha256:three' });
  await nextTurn();
  assert.equal(gets.length, 2);
  assert.equal(gets[0].options.headers['If-None-Match'], 'W/"sha256:old"');

  pending[1]({ ok: true, status: 200, json: async () => ({ revision: 'sha256:three', sources: [], timeline: [{ thread: 'thread:three' }], unavailable: [] }) });
  await nextTurn();
  await nextTurn();
  pending[0]({ ok: true, status: 200, json: async () => ({ revision: 'sha256:two', sources: [], timeline: [{ thread: 'thread:two' }], unavailable: [] }) });
  await nextTurn();
  await nextTurn();

  assert.equal(controller.state.data.operational.revision, 'sha256:three');
  assert.equal(controller.state.data.operational.timeline[0].thread, 'thread:three');
  assert.deepEqual(controller.state.data.inbox, attention);
  controller.componentWillUnmount();
  assert.equal(source.closed, true);
});

test('a failed newest pull does not suppress an older successful snapshot', async () => {
  const pending = [];
  class FakeEventSource {
    static instance;
    constructor() { this.handlers = {}; FakeEventSource.instance = this; }
    addEventListener(name, handler) { this.handlers[name] = handler; }
    close() { this.closed = true; }
  }
  const controller = dashboardController({
    EventSource: FakeEventSource,
    fetch: (_url, options = {}) => {
      if (options.method === 'HEAD') return Promise.resolve({ ok: true });
      return new Promise(resolve => pending.push(resolve));
    }
  });
  controller.state.data = { inbox: [], operational: { revision: 'sha256:old', sources: [], timeline: [], unavailable: [] } };
  controller.startOperationalStream('sha256:old');
  await nextTurn();

  const source = FakeEventSource.instance;
  source.handlers.revision({ data: JSON.stringify({ revision: 'sha256:two' }), lastEventId: 'sha256:two' });
  source.handlers.revision({ data: JSON.stringify({ revision: 'sha256:three' }), lastEventId: 'sha256:three' });
  await nextTurn();
  pending[1]({ ok: false, status: 500 });
  await nextTurn();
  pending[0]({ ok: true, status: 200, json: async () => ({ revision: 'sha256:two', sources: [], timeline: [{ thread: 'thread:two' }], unavailable: [] }) });
  await nextTurn();
  await nextTurn();

  assert.equal(controller.state.data.operational.revision, 'sha256:two');
  assert.equal(controller.state.data.operational.timeline[0].thread, 'thread:two');
  controller.componentWillUnmount();
});

test('operational rows expose honest freshness metadata and navigate from process and work subjects', () => {
  const controller = dashboardController({ fetch: async () => ({ ok: false }), EventSource: null });
  const data = JSON.parse(readFileSync(new URL('../public/demo-data.json', import.meta.url), 'utf8'));
  data.artifactDetails = [{ id: 'XW-atlas-20260803-work', space: 'checkout-core' }];
  data.operational = {
    generated_at: '2026-08-03T12:00:00Z',
    revision: 'sha256:operational', unavailable: [],
    sources: [{ kind: 'space', space: 'checkout-core', freshness: 'stale' }],
    timeline: [{
      space: 'checkout-core', thread: 'thread:atlas-20260803-work', title: 'Contract confidence loop',
      participants: ['atlas', 'checkout'],
      protocol: { settled: false, open_count: 1, waiting_on: ['checkout'], your_move: false, blocking_by: [] },
      latest_milestone: { at: '2026-08-03T11:50:00Z', transition: 'checkpoint', actor: { name: 'codex', system: 'atlas' } },
      consistency: [],
      work: [
        {
          work_id: 'work:01K20ABCDEFHJKMNPQRSTVWXYZ', subject_ref: 'XW-atlas-20260803-work', mode: 'testing',
          summary: 'Running live fixture checks', actor: { name: 'codex', system: 'atlas' }, freshness: 'local-current', current: true,
          reported_at: '2026-08-03T11:45:00Z', observed_at: '2026-08-03T11:59:00Z', valid_until: '2026-08-03T12:15:00Z', waiting_on: []
        },
        {
          work_id: 'work:01K20ABCDEFHJKMNPQRSTVWXYA', subject_ref: 'XC-atlas-events@2.0.0', mode: 'implementing',
          summary: 'Updating contract', actor: { name: 'reviewer', system: 'atlas' }, freshness: 'committed-current', current: true,
          reported_at: '2026-08-03T11:40:00Z', valid_until: '2026-08-03T12:10:00Z', waiting_on: []
        }
      ]
    }]
  };
  controller.state.data = data;

  const values = controller.renderVals();
  assert.equal(values.operationalRows.length, 1);
  assert.match(values.operationalSnapshot, /degraded sources/);
  assert.match(values.operationalRows[0].now, /Current work:/);
  assert.match(values.operationalRows[0].protocol, /checkout/);
  assert.match(values.operationalRows[0].work[0].reported, /^reported /);
  assert.match(values.operationalRows[0].work[0].observed, /^observed /);
  assert.match(values.operationalRows[0].work[0].expires, /^valid until /);

  values.operationalRows[0].work[0].openSubject({ stopPropagation() {} });
  assert.equal(controller.state.mapDocument.id, 'XW-atlas-20260803-work');
  values.operationalRows[0].work[1].openSubject({ stopPropagation() {} });
  assert.equal(controller.state.view, 'contracts');
  assert.equal(controller.state.conSel, 'XC-atlas-events');
  values.operationalRows[0].openThread();
  assert.equal(controller.state.view, 'threads');
  assert.equal(controller.state.threadSel, 'thread:atlas-20260803-work');
});

test('pending recovery is rendered as recovery, never current execution', () => {
  const controller = dashboardController({ fetch: async () => ({ ok: false }), EventSource: null });
  const data = JSON.parse(readFileSync(new URL('../public/demo-data.json', import.meta.url), 'utf8'));
  data.operational = {
    generated_at: '2026-08-03T12:00:00Z', revision: 'sha256:recovery', sources: [], unavailable: [],
    timeline: [{
      space: 'checkout-core', thread: 'thread:recovery', title: 'Recover publication', participants: ['atlas'],
      protocol: { settled: true, open_count: 0, waiting_on: [], your_move: false, blocking_by: [] },
      latest_milestone: {}, consistency: [],
      work: [{ work_id: 'work:01K20ABCDEFHJKMNPQRSTVWXYZ', subject_ref: 'XC-atlas-events@2.0.0', mode: 'paused', summary: 'Needs repair', actor: { name: 'codex', system: 'atlas' }, freshness: 'pending-recovery', current: false, waiting_on: [] }]
    }]
  };
  controller.state.data = data;
  const row = controller.renderVals().operationalRows[0];
  assert.equal(row.now, 'An unfinished publication needs recovery');
  assert.doesNotMatch(row.now, /Current work/);
  assert.equal(row.work[0].freshness, 'publication needs recovery');
});

test('the shared segmented filter bounds wide options inside a horizontal scroller', () => {
  const source = readFileSync(new URL('../design-source/SegmentedFilter.dc.html', import.meta.url), 'utf8');

  assert.match(source, /max-width:calc\(100vw - 56px\)/);
  assert.match(source, /overflow-x:auto/);
  assert.match(source, /width:max-content/);
  assert.match(source, /flex:0 0 auto/);
});

test('the public changelog renders embedded releases before hydration', () => {
  const { logic } = runtimeDesignPage('15-changelog-v4.dc.html');

  assert.match(logic, /const INDEX = window\.A2A_RELEASE_INDEX \|\| \[/);
  assert.match(logic, /state = \{ data: globalThis\.A2A_DEMO \|\| null,/);
  assert.match(logic, /if \(this\.state\.data\) return;/);
  assert.match(logic, /isLatest: INDEX\.length > 0 && r\.version === INDEX\[0\]\[0\]/);
});

test('the public runtime hides raw design markup without collapsing its shell', () => {
  const source = readFileSync(new URL('../src/styles/site.css', import.meta.url), 'utf8');

  assert.match(source, /x-dc \{ display: block; min-height: 100vh; visibility: hidden; \}/);
  assert.match(source, /#dc-root:empty \{ min-height: 100vh; \}/);
});
