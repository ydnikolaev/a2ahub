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
  const projectionSource = readFileSync(new URL('../src/lib/design-source.ts', import.meta.url), 'utf8');
  assert.doesNotMatch(projectionSource, /viewComponents|projectionNames/, 'public projection must derive components from root literal imports');
  const rootSource = readFileSync(new URL('../design-source/14-local-dashboard-v4.dc.html', import.meta.url), 'utf8');
  const resolverImport = rootSource.match(/<dc-import name="(VocabularyResolver)"[^>]*><\/dc-import>/);
  assert.ok(resolverImport, 'root must literally import the vocabulary resolver');
  const resolverLogic = runtimeDesignPage(`${resolverImport[1]}.dc.html`).logic;
  vm.runInNewContext(`(() => { ${resolverLogic}\nreturn Component; })();`, context);
  assert.equal(typeof context.A2A_VOCABULARY_RESOLVER?.lookup, 'function', 'root resolver import must expose one lookup');
  const rootViews = [...rootSource.matchAll(/<dc-import name="([A-Z][A-Za-z0-9]*)" ctx="\{\{ ([A-Za-z][A-Za-z0-9]*) \}\}"[^>]*><\/dc-import>/g)]
    .filter(([, name]) => name !== 'DashboardShell' && name !== 'Modal');
  const projectedDashboard = runtimeDesignPage('14-local-dashboard-v4.dc.html');
  assert.ok(rootViews.length, 'root must expose literal view imports for projection discovery');
  for (const [, name, ctx] of rootViews) {
    assert.doesNotMatch(projectedDashboard.template, new RegExp(`<dc-import name="${name}"`), `${name} markup must be derived from its root import`);
    assert.match(projectedDashboard.logic, new RegExp(`renderPart\\(${name}Projection, values\\.${ctx}\\)`), `${name} logic must be derived from its root context`);
  }
  const componentFiles = {
    Dashboard: '14-local-dashboard-v4.dc.html',
    DashboardShell: 'DashboardShell.dc.html',
    DashboardLive: 'DashboardLive.dc.html',
    Overview: 'Overview.dc.html',
    ExchangeView: 'ExchangeView.dc.html',
    ThreadsView: 'ThreadsView.dc.html',
    ContractsView: 'ContractsView.dc.html',
    MapView: 'MapView.dc.html',
    SpacesView: 'SpacesView.dc.html',
    VersionsView: 'VersionsView.dc.html',
    DocsView: 'DocsView.dc.html',
    GuideView: 'GuideView.dc.html',
    Modal: 'Modal.dc.html',
  };
  const classes = {};
  const logics = {};
  for (const [name, file] of Object.entries(componentFiles)) {
    const raw = name === 'Dashboard' ? readFileSync(new URL(`../design-source/${file}`, import.meta.url), 'utf8') : '';
    const logic = name === 'Dashboard'
      ? raw.match(/<script type="text\/x-dc" data-dc-script[^>]*>([\s\S]*?)<\/script>/)?.[1]
      : runtimeDesignPage(file).logic;
    assert.ok(logic, `${name} must expose design logic`);
    logics[name] = logic;
    vm.runInNewContext(`globalThis.__DashboardPart = (() => { ${logic}\nreturn Component; })();`, context);
    assert.equal(typeof context.__DashboardPart, 'function', `${name} must expose a Component class`);
    classes[name] = context.__DashboardPart;
  }
  assert.doesNotMatch(logics.Dashboard, /DASHBOARD_COMPONENT_FIELDS|const vals = Object\.assign\(base,/, 'root must not retain per-view presentation derivation');
  assert.doesNotMatch(logics.Dashboard, /const operationalRows|const workList|const contractsAll|const releasesShown|docsVals\(/, 'root must retain only shared state, hydration, and cross-view control');
  for (const [name, marker] of Object.entries({ Overview: /const operationalRows/, ExchangeView: /const workList/, ThreadsView: /const tvEntries|tvEntries:/, ContractsView: /const contractVersionOptions/, MapView: /openItem:/, SpacesView: /spaceRows:/, VersionsView: /const releasesShown/, DocsView: /docsVals\(/, GuideView: /guideFeatures:/ })) {
    assert.match(logics[name], marker, `${name} must own its presentation derivation`);
  }
  assert.equal(typeof classes.Modal.prototype.componentDidMount, 'function', 'Modal must own root-overlay ESC lifecycle');
  assert.equal(typeof classes.Modal.prototype.componentWillUnmount, 'function', 'Modal must tear down root-overlay ESC lifecycle');

  const controller = new classes.Dashboard();
  const renderRoot = controller.renderVals.bind(controller);
  const viewNames = ['Overview', 'ExchangeView', 'ThreadsView', 'ContractsView', 'MapView', 'SpacesView', 'VersionsView', 'DocsView', 'GuideView'];
  const views = Object.fromEntries(viewNames.map(name => [name, new classes[name]()]));
  const expectedActions = { Overview: ['navigate', 'patch'], ExchangeView: ['navigate', 'patch'], ThreadsView: ['navigate', 'patch'], ContractsView: ['navigate', 'patch'], MapView: ['navigate', 'patch'], SpacesView: [], VersionsView: ['copy', 'patch'], DocsView: ['patch'], GuideView: [] };
  for (const name of viewNames) {
    const ctx = controller.viewContext(name, {});
    assert.deepEqual(Object.keys(ctx).sort(), ['actions', 'data', 'locale', 'ui'], `${name} must receive exactly one view context`);
    assert.deepEqual(Object.keys(ctx.actions).sort(), expectedActions[name], `${name} must receive only the root callbacks it uses`);
  }
  controller.renderVals = () => {
    const rootValues = renderRoot();
    for (const [name, view] of Object.entries(views)) {
      view.props = { ctx: controller.viewContext(name, rootValues) };
      Object.assign(rootValues, view.renderVals());
    }
    return rootValues;
  };
  const bindViewHelper = (name, method) => (...args) => {
    const view = views[name];
    view.props = { ctx: controller.viewContext(name) };
    if (view.syncContext) view.syncContext();
    return view[method](...args);
  };
  controller.statusOf = bindViewHelper('Overview', 'statusOf');
  controller.detailFor = bindViewHelper('ExchangeView', 'detailFor');
  controller.resolveVocabulary = (data, family, value, locale, policy) => context.A2A_VOCABULARY_RESOLVER.lookup(data, family, value, locale, policy);
  controller.vocabularyPolicy = context.A2A_VOCABULARY_RESOLVER.defaultPolicy;

  const live = new classes.DashboardLive();
  controller.startOperationalStream = initialRevision => {
    live.props = { initialRevision, onSnapshot: snapshot => controller.applyOperationalSnapshot(snapshot) };
    live.startOperationalStream(initialRevision);
  };
  const unmountRoot = controller.componentWillUnmount.bind(controller);
  controller.componentWillUnmount = () => {
    live.componentWillUnmount();
    unmountRoot();
  };
  for (const method of ['renderVals', 'viewContext', 'applyOperationalSnapshot', 'startOperationalStream', 'componentWillUnmount', 'resolveVocabulary']) {
    assert.equal(typeof controller[method], 'function', `dashboard façade is missing ${method}`);
  }
  return controller;
}

const nextTurn = () => new Promise(resolve => setImmediate(resolve));

test('the public Features projection contains only the dashboard Guide screen', () => {
  const { template, logic } = runtimeDesignPage('14-local-dashboard-v4.dc.html', 'guide');

  assert.match(template, /data-screen-label="Guide"/);
  assert.match(template, /data-guide-feedback[\s\S]*name="EmptyState" title="\{\{ feedbackTitle \}\}"/);
  assert.match(template, /data-guide-status-card="true"/);
  assert.match(template, /data-guide-reading-card="true"/);
  assert.doesNotMatch(template, /aria-label="\{\{ guideAria \}\}"/);
  assert.doesNotMatch(template, /readOnlyLabel/);
  assert.doesNotMatch(template, /toggleTypeMenu|integrityFlagsLabel|data-screen-label="Overview"/);
  assert.match(logic, /const DASHBOARD_I18N = \{ en: DASHBOARD_EN \}/);
  assert.match(logic, /const A2A_GLOSSARY = \{ en: A2A_GLOSSARY_EN \}/);
  assert.match(logic, /tone: stateTone\(key\)/);
  assert.match(logic, /const STATE_TONE = \{/);
  assert.doesNotMatch(logic, /key === "blocking"/);
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

const resolverVocabulary = {
  vocabulary: {
    entries: [
      { family:'freshness', value:'stale', labelRU:'отчёт устарел', labelEN:'report expired', explanationRU:'RU work evidence', explanationEN:'EN work evidence', tone:'broken', cue:'×' },
      { family:'source-freshness', value:'stale', labelRU:'источник устарел', labelEN:'source is stale', explanationRU:'RU source evidence', explanationEN:'EN source evidence', tone:'needs-you', cue:'!' },
    ],
    unknown: { labelRU:'неизвестное значение', labelEN:'unknown value', explanationRU:'RU fallback', explanationEN:'EN fallback', tone:'unknown', cue:'?' }
  }
};

test('vocabulary resolver returns the separate fallback without echoing unknown input', () => {
  const controller = dashboardController({ fetch: async () => ({ ok: false }), EventSource: null });
  const got = controller.resolveVocabulary(resolverVocabulary, 'freshness', '<untrusted-raw>', 'en');
  assert.deepEqual(
    { label:got.label, explanation:got.explanation, cue:got.cue },
    { label:'unknown value', explanation:'EN fallback', cue:'?' }
  );
  assert.doesNotMatch(`${got.label}${got.explanation}${got.toneClass}`, /untrusted-raw/);
});

test('vocabulary resolver selects locale and keys colliding values by family', () => {
  const controller = dashboardController({ fetch: async () => ({ ok: false }), EventSource: null });
  const work = controller.resolveVocabulary(resolverVocabulary, 'freshness', 'stale', 'ru');
  const source = controller.resolveVocabulary(resolverVocabulary, 'source-freshness', 'stale', 'en');
  assert.equal(work.label, 'отчёт устарел');
  assert.equal(work.explanation, 'RU work evidence');
  assert.equal(source.label, 'source is stale');
  assert.equal(source.explanation, 'EN source evidence');
});

test('vocabulary resolver shipped policy is emphatic and supports one family override', () => {
  const controller = dashboardController({ fetch: async () => ({ ok: false }), EventSource: null });
  assert.deepEqual(Object.keys(controller.vocabularyPolicy), ['ALL']);
  assert.equal(controller.vocabularyPolicy.ALL, 'emphatic');
  assert.equal(controller.resolveVocabulary(resolverVocabulary, 'freshness', 'stale', 'en').toneClass, 'tone-broken');
  const policy = { ALL:'emphatic', 'source-freshness':'neutral' };
  assert.equal(controller.resolveVocabulary(resolverVocabulary, 'freshness', 'stale', 'en', policy).toneClass, 'tone-broken');
  assert.equal(controller.resolveVocabulary(resolverVocabulary, 'source-freshness', 'stale', 'en', policy).toneClass, 'tone-neutral');
});

test('neutral vocabulary policy preserves words and the non-colour cue', () => {
  const controller = dashboardController({ fetch: async () => ({ ok: false }), EventSource: null });
  const emphatic = controller.resolveVocabulary(resolverVocabulary, 'freshness', 'stale', 'en');
  const neutral = controller.resolveVocabulary(resolverVocabulary, 'freshness', 'stale', 'en', { ALL:'neutral' });
  assert.deepEqual(
    { label:neutral.label, explanation:neutral.explanation, cue:neutral.cue, cueAttribute:neutral.cueAttribute },
    { label:emphatic.label, explanation:emphatic.explanation, cue:emphatic.cue, cueAttribute:emphatic.cueAttribute }
  );
});

test('neutral vocabulary policy collapses only the chroma class', () => {
  const controller = dashboardController({ fetch: async () => ({ ok: false }), EventSource: null });
  const neutral = controller.resolveVocabulary(resolverVocabulary, 'freshness', 'stale', 'en', { ALL:'neutral' });
  assert.equal(neutral.toneClass, 'tone-neutral');
});

test('overview attention teasers route to Exchange and disappear when empty', () => {
  const { template } = runtimeDesignPage('14-local-dashboard-v4.dc.html');
  assert.match(template, /data-overview-attention="true"[\s\S]*\{\{ r\.reasonSentence \}\}[\s\S]*\{\{ r\.waitingOn \}\}[\s\S]*\{\{ r\.expectedMove \}\}/);
  assert.match(template, /<details[\s\S]*\{\{ r\.technicalWhyLabel \}\}[\s\S]*\{\{ r\.technicalWhy \}\}[\s\S]*<\/details>/);
  assert.doesNotMatch(template, /title="\{\{ r\.reasonSentence \}\}"/);

  const controller = dashboardController({ fetch: async () => ({ ok: false }), EventSource: null });
  controller.state.data = JSON.parse(readFileSync(new URL('../../internal/html/testdata/demo.json', import.meta.url), 'utf8'));
  controller.state.view = 'overview';

  let values = controller.renderVals();
  assert.equal(values.hasOvRows, true);
  assert.ok(values.ovRows.length > 0);
  const verdictItem = controller.state.data.inbox.find(item => values.ovRows.some(row => row.id === item.id));
  verdictItem.waitingOn = ['atlas'];
  verdictItem.expectedTransition = 'approve';
  verdictItem.why = 'specs/03-domain.md: diagnostic rule citation';
  verdictItem.blocking = false;
  verdictItem.gatePending = false;
  verdictItem.overdue = true;
  verdictItem.activationOwed = false;
  verdictItem.reasonSentence = { en: 'Atlas must approve this decision.', ru: 'Atlas должен согласовать это решение.' };
  values = controller.renderVals();
  const verdictRow = values.ovRows.find(row => row.id === verdictItem.id);
  assert.equal(verdictRow.reasonSentence, 'Atlas must approve this decision.');
  assert.equal(verdictRow.waitingOn, 'atlas');
  assert.equal(verdictRow.expectedMove, 'approve');
  assert.equal(verdictRow.hasTechnicalWhy, true);
  assert.equal(verdictRow.technicalWhy, verdictItem.why);
  assert.notEqual(verdictRow.reasonSentence, verdictRow.technicalWhy);
  assert.match(verdictRow.status, /overdue/);
  controller.state.locale = 'ru';
  assert.equal(controller.renderVals().ovRows.find(row => row.id === verdictItem.id).reasonSentence, 'Atlas должен согласовать это решение.');
  controller.state.locale = 'en';
  verdictItem.overdue = false;
  verdictItem.activationOwed = true;
  assert.match(controller.statusOf(verdictItem).status, /activation owed/);
  const target = values.ovRows[0];
  target.select();
  assert.equal(controller.state.view, 'work');
  assert.equal(controller.state.workSel, target.id);
  assert.equal(controller.state.workTab, 'incoming');

  controller.state.view = 'overview';
  controller.state.data.inbox = [];
  values = controller.renderVals();
  assert.equal(values.hasOvRows, false);
  assert.deepEqual(Array.from(values.ovRows), []);
});

test('contract version selection switches one atomic package without cross-version facts', () => {
  const controller = dashboardController({ fetch: async () => ({ ok: false }), EventSource: null });
  controller.state.data = JSON.parse(readFileSync(new URL('../../internal/html/testdata/demo.json', import.meta.url), 'utf8'));
  controller.state.view = 'contracts';
  controller.state.conSel = 'XC-atlas-order-envelope';

  let values = controller.renderVals();
  assert.equal(values.cdIsOverview, true);
  assert.equal(values.cdIsVersion, false);

  const riskTarget = values.cdRisks[0];
  assert.match(riskTarget.successorLabel, /Open version 2\.0\.0/);
  riskTarget.openSuccessor();
  assert.equal(controller.state.conSel, 'XC-atlas-order-envelope');
  assert.equal(controller.state.conVersion, '2.0.0');

  controller.setState({ conVersion: '' });
  values = controller.renderVals();
  const currentVersionCard = values.cdVersions.find(version => version.version === '2.2.0');
  assert.equal(currentVersionCard.role, 'button');
  currentVersionCard.openVersion();
  assert.equal(controller.state.conVersion, '2.2.0');

  values = controller.renderVals();
  assert.equal(values.cdIsOverview, false);
  assert.equal(values.cdIsVersion, true);
  assert.equal(values.cdVersionAvailable, true);
  assert.deepEqual(Array.from(values.cdVersionPins, p => p.system), ['checkout']);
  // The version package is a collapsed tree now, not two flat `*Docs` lists.
  // A chain of single-entry directories compacts into one row, so a package
  // opens at `contracts/<contract>/<version>` and its files stay hidden until
  // that row is toggled. Same assertion as before — one atomic package, no
  // file from a sibling version — asked of the shape that actually ships.
  //
  // This test read `cdVersionNormativeDocs`/`cdVersionSupportingDocs`, which
  // the tree replaced; `undefined.every` is not a weaker assertion, it is no
  // assertion, and it went unseen because the web lane is deliberately outside
  // `make check`.
  const packageRoot = (rows, label) => {
    const list = Array.from(rows);
    assert.equal(list.length, 1, `${label}: a version package compacts to one root row`);
    assert.equal(list[0].isDir, true, `${label}: that row is the package directory`);
    return list[0];
  };
  const otherVersions = /\/(1\.5\.0|2\.0\.0|2\.1\.0)(\/|$)/;
  const normRoot = packageRoot(values.cdVersionNormativeTree, 'normative');
  const suppRoot = packageRoot(values.cdVersionSupportingTree, 'supporting');
  assert.match(normRoot.label, /^contracts\/XC-atlas-order-envelope\/2\.2\.0$/);
  assert.match(suppRoot.label, /^contracts\/XC-atlas-order-envelope\/2\.2\.0$/);
  assert.equal(normRoot.expandedAttr, 'false', 'a package folder starts collapsed');

  normRoot.toggle();
  values = controller.renderVals();
  const normFiles = Array.from(values.cdVersionNormativeTree).filter(row => row.isFile);
  assert.ok(normFiles.length > 0, 'expanding the package root reveals its files');
  assert.ok(normFiles.every(file => file.key.includes('|contracts/XC-atlas-order-envelope/2.2.0/')));
  assert.ok(normFiles.every(file => !otherVersions.test(file.key)));

  assert.ok(values.cdVersionFacts.some(row => row.value === '22aa11aa11aa11aa11aa11aa11aa11aa11aa11aa'));
  assert.ok(values.cdVersionFacts.every(row => !String(row.value).includes('21aa11')));
  assert.equal(values.cdVersionProvenanceExpanded, 'false');
  values.toggleCdVersionProvenance();
  values = controller.renderVals();
  assert.equal(values.cdVersionProvenanceExpanded, 'true');

  values.contractVersionSwitcher.options.find(option => option.shortLabel === '1.5.0').go();
  values = controller.renderVals();
  assert.equal(values.cdVersionLegacy, true);
  assert.deepEqual(Array.from(values.cdVersionPins, p => p.system), ['fulfillment']);
  // Switching version opens a fresh, collapsed package: the expansion key
  // carries the directory path, so 2.2.0's open folder is not this one.
  //
  // The assertion that carries the test's title is the one below it. Proven by
  // mutation: widening `selectedConDetail` to every version's documents — the
  // exact leak the component's own comment warns about — reddens these lines
  // and nothing else in the suite.
  const legacyRoot = packageRoot(values.cdVersionNormativeTree, 'normative@1.5.0');
  assert.match(legacyRoot.label, /^contracts\/XC-atlas-order-envelope\/1\.5\.0$/);
  assert.equal(legacyRoot.expandedAttr, 'false');
  legacyRoot.toggle();
  values = controller.renderVals();
  const legacyFiles = Array.from(values.cdVersionNormativeTree).filter(row => row.isFile);
  assert.ok(legacyFiles.length > 0);
  assert.ok(legacyFiles.every(file => file.key.includes('|contracts/XC-atlas-order-envelope/1.5.0/')));
  assert.ok(legacyFiles.every(file => !/\/(2\.0\.0|2\.1\.0|2\.2\.0)(\/|$)/.test(file.key)));

  assert.ok(values.cdVersionFacts.some(row => row.value === '15aa11aa11aa11aa11aa11aa11aa11aa11aa11aa'));
  assert.ok(values.cdVersionFacts.every(row => !String(row.value).includes('22aa11')));

  values.contractVersionSwitcher.options.find(option => option.shortLabel === 'Overview').go();
  values = controller.renderVals();
  const retiredVersionCard = values.cdVersions.find(version => version.version === '1.5.0');
  assert.equal(retiredVersionCard.role, '');
  const successorLink = retiredVersionCard.notes.find(note => note.hasContract);
  successorLink.openContract();
  assert.equal(controller.state.conSel, 'XC-atlas-order-envelope');
  assert.equal(controller.state.conVersion, '2.0.0');
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
  assert.equal(values.operationalRows[0].hasCurrentWork, true);
  assert.equal(values.operationalRows[0].waitingOn, 'checkout');
  assert.match(values.operationalRows[0].currentWork[0].reported, /^reported /);
  assert.match(values.operationalRows[0].currentWork[0].observed, /^observed /);
  assert.match(values.operationalRows[0].currentWork[0].expires, /^valid until /);

  values.operationalRows[0].currentWork[0].openSubject({ stopPropagation() {} });
  assert.equal(controller.state.mapDocument.id, 'XW-atlas-20260803-work');
  values.operationalRows[0].currentWork[1].openSubject({ stopPropagation() {} });
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
  assert.equal(row.hasCurrentWork, false);
  assert.equal(row.currentUnknownText, 'Current work status is unknown: this snapshot has no current report.');
  assert.equal(row.hasHistoricalWork, true);
  assert.match(row.historicalWork[0].actorEvent.meta, /publication needs recovery/);
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
