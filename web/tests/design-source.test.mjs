import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';
import test from 'node:test';
import vm from 'node:vm';
import { injectCardManifest, runtimeDesignPage } from '../src/lib/design-source.ts';

function dashboardController({ fetch, EventSource, hash = '', protocol = 'http:', hostname = '127.0.0.1', demo, focus = null }) {
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
    AbortController,
    document: {
      documentElement,
      body: { dataset: {} },
      activeElement: focus,
      addEventListener() {},
      removeEventListener() {},
      querySelector() { return focus; }
    },
    location: { protocol, hostname, pathname:'/dashboard.html', search: '', hash },
    localStorage: { getItem() { return null; }, setItem() {} },
    navigator: { clipboard: { writeText: async () => {} } }
  };
  context.addEventListener = () => {};
  context.removeEventListener = () => {};
  context.scrollTo = () => {};
  context.scrollX = 0;
  context.scrollY = 0;
  context.requestAnimationFrame = callback => setTimeout(callback, 0);
  context.cancelAnimationFrame = clearTimeout;
  if (demo !== undefined) context.A2A_DEMO = demo;
  context.window = context;
  context.globalThis = context;
  context.history = { replaceState(_state, _title, url) { context.location.hash = ''; context.location.replacedURL = url; } };
  const projectionSource = readFileSync(new URL('../src/lib/design-source.ts', import.meta.url), 'utf8');
  assert.doesNotMatch(projectionSource, /viewComponents|projectionNames/, 'public projection must derive components from root literal imports');
  const rootSource = injectCardManifest(readFileSync(new URL('../design-source/14-local-dashboard-v4.dc.html', import.meta.url), 'utf8'));
  const resolverImport = rootSource.match(/<dc-import name="(VocabularyResolver)"[^>]*><\/dc-import>/);
  assert.ok(resolverImport, 'root must literally import the vocabulary resolver');
  const resolverLogic = runtimeDesignPage(`${resolverImport[1]}.dc.html`).logic;
  vm.runInNewContext(`(() => { ${resolverLogic}\nreturn Component; })();`, context);
  assert.equal(typeof context.A2A_VOCABULARY_RESOLVER?.lookup, 'function', 'root resolver import must expose one lookup');
  const rootViews = [...rootSource.matchAll(/<dc-import name="([A-Z][A-Za-z0-9]*)" ctx="\{\{ ([A-Za-z][A-Za-z0-9]*) \}\}"[^>]*><\/dc-import>/g)]
    .filter(([, name]) => name !== 'DashboardShell' && name !== 'Modal');
  const projectedDashboard = runtimeDesignPage('14-local-dashboard-v4.dc.html');
  assert.doesNotMatch(projectedDashboard.template, /<dc-import name="VocabularyResolver"/, 'public projection must not race a support-only import');
  assert.match(projectedDashboard.logic, /globalThis\.A2A_VOCABULARY_RESOLVER = A2A_VOCABULARY_RESOLVER;/, 'public projection must initialize support-only imports before root render');
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
    const raw = name === 'Dashboard' ? injectCardManifest(readFileSync(new URL(`../design-source/${file}`, import.meta.url), 'utf8')) : '';
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
  for (const [name, marker] of Object.entries({ Overview: /const operationalRows/, ExchangeView: /const workList/, ThreadsView: /const tvEntries|tvEntries:/, ContractsView: /const contractVersionOptions/, MapView: /openContract:/, SpacesView: /spaceRows:/, VersionsView: /const releasesShown/, DocsView: /docsVals\(/, GuideView: /guideFeatures:/ })) {
    assert.match(logics[name], marker, `${name} must own its presentation derivation`);
  }
  assert.equal(typeof classes.Modal.prototype.componentDidMount, 'function', 'Modal must own root-overlay ESC lifecycle');
  assert.equal(typeof classes.Modal.prototype.componentWillUnmount, 'function', 'Modal must tear down root-overlay ESC lifecycle');

  const controller = new classes.Dashboard();
  const renderRoot = controller.renderVals.bind(controller);
  const viewNames = ['Overview', 'ExchangeView', 'ThreadsView', 'ContractsView', 'MapView', 'SpacesView', 'VersionsView', 'DocsView', 'GuideView'];
  const views = Object.fromEntries(viewNames.map(name => [name, new classes[name]()]));
  const cardActions = ['cardAccent', 'cardID', 'openCard'];
  const expectedActions = {
    Overview: [...cardActions, 'navigate', 'openAggregate', 'patch'].sort(),
    ExchangeView: [...cardActions, 'navigate', 'patch'].sort(),
    ThreadsView: [...cardActions, 'navigate', 'patch'].sort(),
    ContractsView: [...cardActions, 'copy', 'navigate', 'patch', 'sourceURL'].sort(),
    MapView: [...cardActions, 'navigate', 'patch'].sort(),
    SpacesView: cardActions,
    VersionsView: ['copy', 'patch'], DocsView: ['patch'], GuideView: []
  };
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
  controller.overviewValues = (ui = {}) => {
    const view = views.Overview;
    const base = controller.viewContext('Overview');
    view.props = { ctx: { ...base, ui: { ...base.ui, ...ui } } };
    return view.renderVals();
  };
  controller.threadsValues = (ui = {}) => {
    const view = views.ThreadsView;
    const base = controller.viewContext('ThreadsView');
    view.props = { ctx: { ...base, ui: { ...base.ui, ...ui } } };
    return view.renderVals();
  };
  controller.contractsValues = (ui = {}) => {
    const view = views.ContractsView;
    const base = controller.viewContext('ContractsView');
    view.props = { ctx: { ...base, ui: { ...base.ui, ...ui } } };
    return view.renderVals();
  };
  controller.detailFor = bindViewHelper('ExchangeView', 'detailFor');
  controller.resolveVocabulary = (data, family, value, locale, policy) => context.A2A_VOCABULARY_RESOLVER.lookup(data, family, value, locale, policy);
  controller.knowsVocabulary = (data, family, value) => context.A2A_VOCABULARY_RESOLVER.knows(data, family, value);
  controller.vocabularyPolicy = context.A2A_VOCABULARY_RESOLVER.defaultPolicy;

  const live = new classes.DashboardLive();
  controller.startOperationalStream = data => {
    live.props = {
      data: data || controller.state.data,
      onHydrate: candidate => controller.hydrate(candidate),
      onState: liveTransport => controller.setState({ liveTransport }),
      onReady: refresh => controller.setState({ refresh })
    };
    live.startOperationalStream(live.props.data);
  };
  const unmountRoot = controller.componentWillUnmount.bind(controller);
  controller.componentWillUnmount = () => {
    live.componentWillUnmount();
    unmountRoot();
  };
  for (const method of ['renderVals', 'viewContext', 'startOperationalStream', 'componentWillUnmount', 'resolveVocabulary', 'knowsVocabulary', 'cardID', 'cardAccent', 'resolveCard', 'openCard', 'closeCard', 'openAggregate', 'sourceURL']) {
    assert.equal(typeof controller[method], 'function', `dashboard façade is missing ${method}`);
  }
  controller.testLocation = context.location;
  controller.liveController = live;
  controller.testContext = context;
  controller.testClasses = classes;
  return controller;
}

const nextTurn = () => new Promise(resolve => setImmediate(resolve));
const digest = character => `sha256:${character.repeat(64)}`;
const etag = value => `W/"${value}"`;
const responseHeaders = value => ({ get(name) { return String(name).toLowerCase() === 'etag' ? value : null; } });
const dashboardData = () => JSON.parse(readFileSync(new URL('../public/demo-data.json', import.meta.url), 'utf8'));

function dashboardImportGraph() {
  const pending = ['14-local-dashboard-v4.dc.html'];
  const sources = new Map();
  while (pending.length) {
    const file = pending.shift();
    if (sources.has(file)) continue;
    const source = readFileSync(new URL(`../design-source/${file}`, import.meta.url), 'utf8');
    sources.set(file, source);
    for (const match of source.matchAll(/<dc-import name="([A-Z][A-Za-z0-9]*)"/g)) {
      const child = `${match[1]}.dc.html`;
      if (!sources.has(child)) pending.push(child);
    }
  }
  return sources;
}

test('DashboardLive is the only network owner in the local-dashboard import graph', () => {
  const owners = [];
  for (const [file, source] of dashboardImportGraph()) {
    if (/\b(?:fetch|EventSource|WebSocket)\s*\(/.test(source)) owners.push(file);
  }
  assert.deepEqual(owners, ['DashboardLive.dc.html']);
});

test('dashboard card seam is manifest-backed and keeps identity opaque', () => {
  const manifest = JSON.parse(readFileSync(new URL('../design-source/cards.manifest.json', import.meta.url), 'utf8'));
  assert.deepEqual(manifest.cards.map(card => card.kind), ['item', 'thread', 'contract', 'cver', 'work-report', 'space', 'operational-process']);
  assert.deepEqual(manifest.cards.map(card => card.component), ['ArtifactDetail', 'ThreadsView', 'ContractsView', 'ContractsView', 'ExchangeView', 'SpacesView', 'Overview']);
  assert.deepEqual(manifest.cards.map(card => card.identityFields), [['space','id'], ['space','thread'], ['space','id'], ['space','id','version'], ['space','artifact_id'], ['id'], ['space','thread']]);
  assert.deepEqual(manifest.cards.map(card => card.selectionPaths), [
    [['inbox'],['outbox'],['archive']], [['threadViews']], [['contracts']], [['contracts','versions']],
    [['workReports']], [['spaces']], [['operational','timeline']]
  ]);
  assert.deepEqual(manifest.cards.map(card => card.accentFamilies), [
    ['state','owner','move','event','reference','attachment','operational-item'],
    ['member','open-item','participant','event','reference','delivery'],
    ['version','consumer','dependency-line'], ['consumer-pin','dependency-line','document','history-event','successor'],
    ['actor','wait','subject','checkpoint'], ['participant','source','consistency-anomaly'],
    ['milestone','work-row','wait','consistency-anomaly','source']
  ]);
  assert.equal(new Set(manifest.cards.map(card => card.component)).size, 6, 'seven kinds must resolve through the six declared family components');
  assert.ok(runtimeDesignPage('14-local-dashboard-v4.dc.html').logic.includes(`const A2A_CARD_MANIFEST = ${JSON.stringify(manifest)};`), 'source projection must carry the exact parsed manifest bytes');

  const controller = dashboardController({ fetch: async () => ({ ok: false }), EventSource: null });
  for (const method of ['cardID', 'cardAccent', 'resolveCard', 'openCard', 'closeCard', 'openAggregate', 'sourceURL']) {
    assert.equal(typeof controller[method], 'function', `dashboard card seam is missing ${method}`);
  }
  for (const name of ['Overview','ExchangeView','ThreadsView','ContractsView','MapView','SpacesView']) {
    assert.equal(Object.hasOwn(controller.viewContext(name).ui, 'card'), false, `${name} normal mode must remain card-free`);
  }
  const opaque = controller.cardID('item', { space: 'space/a', id: 'XQ:1' });
  assert.equal(typeof opaque, 'string');
  assert.doesNotMatch(opaque, /space\/a|XQ:1/, 'opaque card ids must not expose tuple delimiters or values');
  assert.equal(controller.cardID('item', { space:'space/a', id:'XQ:1', ignored:'one' }), controller.cardID('item', { space:'space/a', id:'XQ:1', ignored:'two' }));
  assert.notEqual(controller.cardID('item', { space:'space/b', id:'XQ:1' }), opaque);
  assert.notEqual(controller.cardID('item', { space:'space/a', id:'XQ:2' }), opaque);
  assert.equal(controller.cardID('item', { space:'space/a' }), '');
  const cfile = controller.cardID('cfile', { space:'space/a', id:'XC:1', version:'2.0.0', path:'schema/openapi.yaml' });
  assert.ok(cfile, 'reserved cfile resources need the same opaque identity constructor');
  assert.doesNotMatch(cfile, /space\/a|XC:1|2\.0\.0|openapi/, 'reserved cfile identity must not expose its tuple');
  assert.notEqual(controller.cardID('cfile', { space:'space/a', id:'XC:1', version:'2.0.0', path:'README.md' }), cfile);
  assert.equal(controller.cardID('cfile', { space:'space/a', id:'XC:1', version:'2.0.0' }), '');
  assert.equal(controller.openCard('cfile', cfile, ''), false, 'reserved cfile identity is not an eighth card kind');

  const data = JSON.parse(readFileSync(new URL('../../internal/html/testdata/demo.json', import.meta.url), 'utf8'));
  const contract = data.contracts[0];
  const cardCases = [
    ['item', data.inbox[0]],
    ['thread', data.threadViews[0]],
    ['contract', contract],
    ['cver', { space:contract.space, id:contract.id, version:contract.versions[0].version }],
    ['work-report', data.workReports[0]],
    ['space', data.spaces[0]],
    ['operational-process', data.operational.timeline[0]]
  ];
  for (const [kind, source] of cardCases) {
    const selection = { kind, id:controller.cardID(kind, source), accent:controller.cardAccent('fixture', [kind]) };
    assert.strictEqual(controller.resolveCard(data, selection), selection, `${kind} must resolve through manifest selectionPaths`);
    assert.equal(controller.resolveCard(data, { ...selection, id:selection.id + '-absent' }), null, `${kind} must fail closed when absent`);
  }

  const itemSelection = { kind:'item', id:controller.cardID('item', data.inbox[0]), accent:'preserved-accent' };
  const movedItemData = structuredClone(data);
  const movedItem = movedItemData.inbox.shift();
  movedItemData.archive.push(movedItem);
  assert.strictEqual(controller.resolveCard(movedItemData, itemSelection), itemSelection, 'an item moving between carried collections remains selected');
  movedItemData.outbox.push(structuredClone(movedItem));
  assert.strictEqual(controller.resolveCard(movedItemData, itemSelection), itemSelection, 'duplicate carried item membership is existence-only');
  movedItemData.archive = movedItemData.archive.filter(item => controller.cardID('item', item) !== itemSelection.id);
  movedItemData.outbox = movedItemData.outbox.filter(item => controller.cardID('item', item) !== itemSelection.id);
  assert.equal(controller.resolveCard(movedItemData, itemSelection), null, 'removing an item from every manifest path makes it gone');

  const oldVersion = contract.versions[0];
  const versionSelection = { kind:'cver', id:controller.cardID('cver', { space:contract.space, id:contract.id, version:oldVersion.version }), accent:'document-accent' };
  assert.strictEqual(controller.resolveCard(data, versionSelection), versionSelection, 'a nested non-current version resolves from parent and leaf identity');
  const withoutOldVersion = structuredClone(data);
  withoutOldVersion.contracts[0].versions = withoutOldVersion.contracts[0].versions.filter(version => version.version !== oldVersion.version);
  assert.equal(controller.resolveCard(withoutOldVersion, versionSelection), null, 'removing only the selected version makes cver gone while the contract survives');

  const threadSource = data.threadViews[0];
  const threadSelection = { kind:'thread', id:controller.cardID('thread', threadSource), accent:'' };
  const threadsWithoutView = structuredClone(data);
  threadsWithoutView.threadViews = threadsWithoutView.threadViews.filter(view => view.space !== threadSource.space || view.thread !== threadSource.thread);
  assert.equal(controller.resolveCard(threadsWithoutView, threadSelection), null, 'threads[].id alone cannot resolve the card surface without threadViews[].thread');

  const report = data.workReports[0];
  const reportSelection = { kind:'work-report', id:controller.cardID('work-report', report), accent:'' };
  const changedWorkID = structuredClone(data);
  changedWorkID.workReports[0].work_id = 'work:changed';
  assert.strictEqual(controller.resolveCard(changedWorkID, reportSelection), reportSelection, 'work_id is not part of work-report card identity');
  changedWorkID.workReports[0].artifact_id = 'XA-changed';
  assert.equal(controller.resolveCard(changedWorkID, reportSelection), null, 'artifact_id is part of work-report card identity');

  const process = data.operational.timeline[0];
  const processSelection = { kind:'operational-process', id:controller.cardID('operational-process', process), accent:'' };
  const sourcesOnly = structuredClone(data);
  sourcesOnly.operational.timeline = [];
  assert.equal(controller.resolveCard(sourcesOnly, processSelection), null, 'operational sources cannot stand in for timeline processes');
  assert.equal(controller.resolveCard(data, { kind:'future-kind', id:'opaque', accent:'' }), null);
  assert.equal(controller.resolveCard(data, { kind:'cfile', id:cfile, accent:'' }), null, 'reserved resources are not manifest cards');

  const rootLogic = runtimeDesignPage('14-local-dashboard-v4.dc.html').logic;
  const resolveCardSource = rootLogic.match(/resolveCard\(data, selection\) \{([\s\S]*?)\n  \}/)?.[1] || '';
  assert.ok(resolveCardSource, 'root must expose one manifest-backed resolveCard method');
  assert.doesNotMatch(resolveCardSource, /switch\s*\(|kind\s*===|decodeOpaqueTuple/, 'resolveCard must stay generic and must not decode opaque ids');
  controller.openCard('item', opaque, controller.cardAccent('state', ['published', 'XQ:1']));
  assert.deepEqual(Object.keys(controller.state.card).sort(), ['accent', 'id', 'kind']);
  assert.match(controller.testLocation.hash, /^#card=/);
  const savedCardHash = controller.testLocation.hash;
  const reloaded = dashboardController({ fetch: async () => ({ ok:false }), EventSource:null, hash:savedCardHash });
  reloaded.restoreCardFromHash();
  assert.equal(JSON.stringify(reloaded.state.card), JSON.stringify(controller.state.card), 'reload restores the exact opaque card tuple');
  controller.openCard('item', opaque, controller.cardAccent('version', 'v1'));
  assert.equal(controller.state.card.accent, '', 'an accent allowed for another kind must not alter this card');
  assert.equal(controller.cardAccent('state', [{ raw: true }]), '', 'accent identity is limited to JSON-safe carried scalars');
  controller.testLocation.hash = '#ordinary-section';
  controller.restoreCardFromHash();
  assert.equal(controller.state.card, null, 'browser back or another hash clears stale card state');
  const unknownKind = dashboardController({ fetch: async () => ({ ok:false }), EventSource:null, hash:'#card=' + encodeURIComponent(JSON.stringify(['future-kind','opaque',''])) });
  unknownKind.restoreCardFromHash();
  assert.equal(JSON.stringify(unknownKind.state.card), JSON.stringify({ kind:'future-kind', id:'opaque', accent:'' }));
  const unknownAccent = dashboardController({ fetch: async () => ({ ok:false }), EventSource:null, hash:'#card=' + encodeURIComponent(JSON.stringify(['item',opaque,controller.cardAccent('version','v1')])) });
  unknownAccent.restoreCardFromHash();
  assert.equal(JSON.stringify(unknownAccent.state.card), JSON.stringify({ kind:'item', id:opaque, accent:'' }), 'unknown accent keeps the selected kind/id and drops only the highlight');
  const malformed = dashboardController({ fetch: async () => ({ ok:false }), EventSource:null, hash:'#card=not-json' });
  malformed.restoreCardFromHash();
  assert.equal(JSON.stringify(malformed.state.card), JSON.stringify({ kind:'', id:'', accent:'' }));

  for (const [key, view] of [['needYou','work'], ['noHumanMove','work'], ['youAwait','work'], ['linesOffCurrent','contracts']]) {
    assert.equal(controller.openAggregate(key), true);
    assert.deepEqual({ view: controller.state.view, dashboardSet: controller.state.dashboardSet }, { view, dashboardSet:key });
  }
  controller.openAggregate('needYou');
  assert.equal(controller.uiSlice('Overview').dashboardSet, undefined);
  assert.equal(controller.uiSlice('ExchangeView').dashboardSet, 'needYou');
  assert.equal(controller.uiSlice('ContractsView').dashboardSet, 'needYou');
  const beforeUnknownSet = { view:controller.state.view, dashboardSet:controller.state.dashboardSet };
  assert.equal(controller.openAggregate('unknown-set'), false);
  assert.deepEqual({ view:controller.state.view, dashboardSet:controller.state.dashboardSet }, beforeUnknownSet);

  controller.state.data = { spaces: [{ id: 'space-a', repoURL: 'git@github.com:owner/repo.git', revision: 'abc123' }] };
  for (const repoURL of ['https://github.com/owner/repo.git', 'git@github.com:owner/repo.git', 'ssh://git@github.com/owner/repo.git']) {
    controller.state.data.spaces[0].repoURL = repoURL;
    assert.equal(controller.sourceURL('space-a', 'contracts/v1/schema.json', 'abc123'), 'https://github.com/owner/repo/blob/abc123/contracts/v1/schema.json');
  }
  assert.equal(controller.sourceURL('space-a', '../secret', 'abc123'), '');
  assert.equal(controller.sourceURL('space-a', '/absolute', 'abc123'), '');
  assert.equal(controller.sourceURL('space-a', 'bad\\path', 'abc123'), '');
  assert.equal(controller.sourceURL('space-a', 'contracts/v1/schema.json', 'different'), '');
  controller.state.data.spaces[0].repoURL = 'https://gitlab.com/owner/repo';
  assert.equal(controller.sourceURL('space-a', 'contracts/v1/schema.json', 'abc123'), '');
  controller.state.data.spaces[0].repoURL = 'https://github.com/owner/repo?tab=readme';
  assert.equal(controller.sourceURL('space-a', 'contracts/v1/schema.json', 'abc123'), '');
  controller.state.data.spaces[0].repoURL = 'https://github.com/owner/repo';
  controller.state.data.spaces[0].revision = '';
  assert.equal(controller.sourceURL('space-a', 'contracts/v1/schema.json'), '', 'source links require a carried revision and never invent HEAD');
  assert.equal(controller.sourceURL('space-a', 'contracts/v1/schema.json', 'caller-only'), '', 'a caller revision is never authority when the carried revision is empty');
  controller.state.data.spaces[0] = { id:'space-a', repoURL:'https://github.com/../repo', revision:'abc123' };
  assert.equal(controller.sourceURL('space-a', 'contracts/v1/schema.json', 'abc123'), '');
  controller.state.data.spaces[0].repoURL = 'https://github.com/owner/..';
  assert.equal(controller.sourceURL('space-a', 'contracts/v1/schema.json', 'abc123'), '');
  controller.openCard('item', opaque, '');
  controller.closeCard();
  assert.equal(controller.state.card, null);
  assert.equal(controller.testLocation.hash, '');
  assert.equal(controller.testLocation.replacedURL, '/dashboard.html');
  const ordinaryHash = dashboardController({ fetch: async () => ({ ok:false }), EventSource:null, hash:'#guide' });
  ordinaryHash.state.card = { kind:'item', id:opaque, accent:'' };
  ordinaryHash.closeCard();
  assert.equal(ordinaryHash.testLocation.hash, '#guide', 'closeCard removes only its own #card hash');
});

test('card manifest is identical in source, public projection, and self-contained output', () => {
  const manifest = JSON.parse(readFileSync(new URL('../design-source/cards.manifest.json', import.meta.url), 'utf8'));
  const declaration = `const A2A_CARD_MANIFEST = ${JSON.stringify(manifest)};`;
  const raw = readFileSync(new URL('../design-source/14-local-dashboard-v4.dc.html', import.meta.url), 'utf8');
  const projectionSource = readFileSync(new URL('../src/lib/design-source.ts', import.meta.url), 'utf8');
  const buildSource = readFileSync(new URL('../scripts/build-local-dashboard.mjs', import.meta.url), 'utf8');
  const injected = injectCardManifest(raw);
  const projected = runtimeDesignPage('14-local-dashboard-v4.dc.html').logic;
  for (const validator of [projectionSource, buildSource]) {
    assert.match(validator, /row\.selectionPaths/);
    assert.match(validator, /malformed selectionPaths/);
  }
  assert.ok(injected.includes(declaration));
  assert.ok(projected.includes(declaration));
  assert.doesNotMatch(injected, /\/\*A2A_CARD_MANIFEST\*\/null/);
  assert.doesNotMatch(projected, /\/\*A2A_CARD_MANIFEST\*\/null/);

  const temp = mkdtempSync(join(tmpdir(), 'a2a-card-manifest-'));
  const output = join(temp, 'dashboard.html');
  try {
    const built = spawnSync(process.execPath, [fileURLToPath(new URL('../scripts/build-local-dashboard.mjs', import.meta.url))], {
      cwd: new URL('..', import.meta.url),
      env: { ...process.env, A2A_DASHBOARD_OUTPUT:output },
      encoding:'utf8'
    });
    assert.equal(built.status, 0, `${built.stderr}\n${built.stdout}`);
    const selfContained = readFileSync(output, 'utf8');
    assert.ok(selfContained.includes(declaration));
    assert.doesNotMatch(selfContained, /\/\*A2A_CARD_MANIFEST\*\/null/);
  } finally {
    rmSync(temp, { recursive:true, force:true });
  }
});

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
  assert.equal(controller.knowsVocabulary(resolverVocabulary, 'freshness', 'stale'), true);
  assert.equal(controller.knowsVocabulary(resolverVocabulary, 'freshness', '<untrusted-raw>'), false);
  assert.deepEqual(
    { label:got.label, explanation:got.explanation, cue:got.cue },
    { label:'unknown value', explanation:'EN fallback', cue:'?' }
  );
  assert.doesNotMatch(`${got.label}${got.explanation}${got.toneClass}`, /untrusted-raw/);
});

test('transition result stays dominant through the shared event-signal primitive', () => {
  const actor = readFileSync(new URL('../design-source/ActorEventLine.dc.html', import.meta.url), 'utf8');
  const signal = readFileSync(new URL('../design-source/EventSignal.dc.html', import.meta.url), 'utf8');
  assert.match(actor, /EventSignal" result="\{\{ signalResult \}\}" tone="\{\{ signalTone \}\}"/);
  assert.doesNotMatch(actor, /tone-settled|tone-broken|tone-needs-you|tone-waiting-them|tone-progressing|tone-unknown/);
  assert.match(signal, /const result = this\.props\.result/);
  assert.match(signal, /result\.explanation \|\| result\.label/);
  assert.match(signal, /result\.cueAttribute \|\| result\.cue/);
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

test('P9 Overview consumes carried aggregates and renders obligations and explicit attention emptiness', () => {
  // Operator decision: an empty attention heading is now valuable evidence
  // that the carried set is complete and empty, rather than visual clutter.
  const { template } = runtimeDesignPage('14-local-dashboard-v4.dc.html');
  assert.match(template, /data-overview-attention="true"[\s\S]*\{\{ r\.reasonSentence \}\}[\s\S]*\{\{ r\.waitingOn \}\}[\s\S]*\{\{ r\.expectedMove \}\}/);
  assert.match(template, /<details[\s\S]*\{\{ r\.technicalWhyLabel \}\}[\s\S]*\{\{ r\.technicalWhy \}\}[\s\S]*<\/details>/);
  assert.doesNotMatch(template, /title="\{\{ r\.reasonSentence \}\}"/);
  assert.match(template, /data-overview-attention-empty="true"[\s\S]*EmptyState/);

  const source = readFileSync(new URL('../design-source/Overview.dc.html', import.meta.url), 'utf8');
  assert.doesNotMatch(source, /\bblockedWhy\b|\bstatusOf\b|\bSTATE_TONE\b|\bconst settled\b/);
  assert.doesNotMatch(source, /\.sort\s*\(/, 'Overview must preserve carried order');
  assert.doesNotMatch(source, /scopedInbox|restCount|snapshotEventCount|workReportCount|inventoryTypes|offCurrent|scopedEdges/);
  assert.match(source, /A2A_VOCABULARY_RESOLVER\.lookup/);
  assert.match(source, /data-overview-kpi="\{\{ s\.key \}\}"[\s\S]*<button[^>]*aria-label="\{\{ s\.accessibleName \}\}"[\s\S]*<\/button>[\s\S]*<dc-import name="HintDot"/, 'KPI action and explanation must be sibling controls');
  const kpiMarkup = source.slice(source.indexOf('data-overview-kpi="{{ s.key }}"'), source.indexOf('</sc-for>', source.indexOf('data-overview-kpi="{{ s.key }}"')));
  assert.ok(kpiMarkup.indexOf('</button>') < kpiMarkup.indexOf('<dc-import name="HintDot"'), 'KPI action must close before the interactive HintDot');
  assert.doesNotMatch(source, /cardFactOrder/, 'P5 order must be proven by rendered structure, not detached metadata');
  assert.doesNotMatch(source, /vocabulary\([^)]*"transition"[^)]*expectedTransition/, 'future ExpectedTransition must remain exact carried copy');
  const processMarkers = [
    'data-operational-protocol',
    'data-operational-participants',
    'data-operational-latest-milestone',
    'data-operational-work',
    'data-operational-waits-checkpoint',
    'data-operational-shared-pending',
    'data-operational-consistency',
    'data-operational-source',
    'data-operational-windows'
  ];
  const processOffsets = processMarkers.map(marker => template.indexOf(marker));
  assert.ok(processOffsets.every(offset => offset >= 0), 'compiled Overview DOM must carry every P5 operational-process group marker');
  assert.deepEqual([...processOffsets].sort((a, b) => a - b), processOffsets, 'compiled Overview DOM must follow exact P5 group order');
  for (const marker of processMarkers) {
    assert.equal(template.split(marker).length - 1, 1, `${marker} must render once per process rather than once per work row`);
  }
  const workGroup = template.slice(processOffsets[3], processOffsets[4]);
  const waitsGroup = template.slice(processOffsets[4], processOffsets[5]);
  const pendingGroup = template.slice(processOffsets[5], processOffsets[6]);
  assert.doesNotMatch(workGroup, /checkpointText|\{\{ w\.waits \}\}|hasSharedPending|sharedPendingText/, 'work rows must not interleave later P5 groups');
  assert.match(waitsGroup, /sc-for list="\{\{ r\.work \}\}"[\s\S]*checkpointText[\s\S]*sc-for list="\{\{ w\.waits \}\}"[\s\S]*\{\{ wait\.text \}\}[\s\S]*<details[\s\S]*\{\{ wait\.technical \}\}/);
  assert.doesNotMatch(waitsGroup, /hasSharedPending|sharedPendingText/, 'shared pending must follow the complete waits/checkpoint group');
  assert.match(pendingGroup, /sc-for list="\{\{ r\.work \}\}"[\s\S]*hasSharedPending[\s\S]*sharedPendingText/);
  assert.equal(template.split('{{ wait.text }}').length - 1, 1, 'each carried wait has one rendering path');
  assert.equal(template.split('{{ w.sharedPendingText }}').length - 1, 1, 'each carried shared-pending action has one rendering path');
  assert.doesNotMatch(template, /\{\{ r\.yourMove(?:Label)? \}\}/, 'Protocol.YourMove is viewer chrome, not portable card text');

  const controller = dashboardController({ fetch: async () => ({ ok: false }), EventSource: null });
  controller.state.data = JSON.parse(readFileSync(new URL('../../internal/html/demo-data.json', import.meta.url), 'utf8'));
  controller.state.view = 'overview';

  const [verdictItem, secondItem] = controller.state.data.inbox;
  verdictItem.waitingOn = ['atlas', 'checkout'];
  verdictItem.expectedTransition = 'approve';
  verdictItem.why = 'specs/03-domain.md: diagnostic rule citation';
  verdictItem.reasonSentence = { en: 'Atlas must approve this decision.', ru: 'Atlas должен согласовать это решение.' };
  secondItem.waitingOn = ['billing'];
  secondItem.expectedTransition = 'verify';
  secondItem.reasonSentence = { en:'Billing must verify the replay.', ru:'Billing должен проверить повтор.' };
  controller.state.data.aggregates = {
    needYou:{ items:[secondItem, verdictItem], window:{ shown:2, total:17, truncated:true } },
    noHumanMove:{ items:[verdictItem], window:{ shown:1, total:31, truncated:true } },
    youAwait:{ items:[secondItem], window:{ shown:1, total:23, truncated:true } },
    linesOffCurrent:{ items:[controller.state.data.contractEdges[0]], window:{ shown:1, total:11, truncated:true }, complete:true, degradations:[] }
  };
  controller.state.data.windows = {
    inbox:{shown:9,total:101,truncated:true}, outbox:{shown:8,total:102,truncated:true},
    archive:{shown:7,total:103,truncated:true}, threads:{shown:6,total:104,truncated:true},
    workReports:{shown:5,total:105,truncated:true}, artifactDetails:{shown:4,total:106,truncated:true}
  };
  const process = controller.state.data.operational.timeline[0];
  process.work[0].waiting_on = [{ kind:'artifact', id:'wait:first', summary:'First carried wait' }];
  process.work[1].waiting_on = [{ kind:'contract', id:'wait:second', summary:'Second carried wait' }];

  let values = controller.overviewValues();
  assert.deepEqual(Array.from(values.ovStats, stat => [stat.key, stat.value]), [
    ['needYou',17], ['noHumanMove',31], ['youAwait',23], ['linesOffCurrent',11]
  ]);
  for (const stat of values.ovStats) {
    assert.match(stat.accessibleName, new RegExp(`${stat.label}.*${stat.value}`));
    controller.state.view = 'overview';
    stat.open();
    assert.equal(controller.state.dashboardSet, stat.key);
  }
  assert.deepEqual(Array.from(values.inventoryRows, row => row.value), [101,102,103,104,105,106]);
  assert.deepEqual(Array.from(values.ovRows, row => row.id), [secondItem.id, verdictItem.id], 'attention rows preserve the carried order');
  controller.state.data.inbox = [];
  controller.state.data.outbox = [];
  controller.state.data.contractEdges = [];
  values = controller.overviewValues();
  assert.deepEqual(Array.from(values.ovStats, stat => stat.value), [17,31,23,11], 'raw collection changes cannot redefine carried KPI totals');
  assert.deepEqual(Array.from(values.ovRows, row => row.id), [secondItem.id, verdictItem.id], 'raw collection changes cannot redefine carried attention membership');
  const verdictRow = values.ovRows[1];
  assert.equal(verdictRow.reasonSentence, 'Atlas must approve this decision.');
  assert.equal(verdictRow.waitingOn, 'atlas and checkout');
  assert.equal(verdictRow.expectedMove, 'approve');
  assert.notEqual(verdictRow.expectedMove, 'approved', 'future ExpectedTransition must not receive the event-history label');
  assert.equal(verdictRow.hasTechnicalWhy, true);
  assert.equal(verdictRow.technicalWhy, verdictItem.why);
  assert.notEqual(verdictRow.reasonSentence, verdictRow.technicalWhy);
  assert.equal(verdictRow.statusResult.label, 'Submitted for review');
  verdictRow.openCard();
  assert.equal(controller.state.card.kind, 'item');
  controller.state.locale = 'ru';
  assert.equal(controller.overviewValues().ovRows.find(row => row.id === verdictItem.id).reasonSentence, 'Atlas должен согласовать это решение.');
  controller.state.locale = 'en';

  const processRow = values.operationalRows.find(row => row.thread === process.thread && row.space === process.space);
  assert.ok(processRow.work.length >= 2, 'the P5 grouping tooth requires multiple carried work rows');
  assert.deepEqual(Array.from(processRow.work.slice(0, 2), row => row.waits.length), [1, 1]);
  assert.equal(processRow.obligationsGridVisible, true);
  assert.equal(processRow.waitingOn, 'atlas');
  assert.equal(processRow.blockingBy, 'atlas');
  assert.equal(processRow.protocolResult.label, 'Still open');
  assert.equal(processRow.work[0].modeResult.label, 'Implementing now');
  assert.equal(processRow.work[0].freshnessResult.label, 'observed locally');
  assert.equal(processRow.consistency[0].result.label, 'Sources need review');
  assert.equal(processRow.sourceResult.label, 'Source is current');
  assert.equal(processRow.work[0].waits[0].text, 'First carried wait');
  assert.match(processRow.work[0].waits[0].technical, /artifact/);
  assert.match(processRow.work[0].waits[0].technical, /wait:first/);
  assert.doesNotMatch(processRow.work[0].waits[0].text, /artifact|wait:first/, 'raw wait kind/id stay technical');
  assert.equal(processRow.milestoneResult.label, 'submitted for review');
  assert.match(processRow.milestoneText, /submitted for review/);
  assert.doesNotMatch(processRow.milestoneText, /\bsubmit\b/, 'raw milestone transition must not leak into visible copy');
  assert.equal(processRow.workWindowText, 'Showing 16 of 36.');
  assert.equal(processRow.consistencyWindowText, 'Showing 32 of 33.');
  const processID = controller.cardID('operational-process', process);
  processRow.openCard();
  assert.equal(controller.state.card.kind, 'operational-process');
  assert.equal(controller.state.card.id, processID);
  const cardValues = controller.overviewValues({ card:{kind:'operational-process', id:processID, accent:''} });
  assert.equal(cardValues.cardMode, true);
  assert.equal(cardValues.cardProcess.thread, process.thread);
  const workAccent = controller.cardAccent('work-row', process.work[0].work_id);
  const accentedCard = controller.overviewValues({ card:{kind:'operational-process', id:processID, accent:workAccent} });
  assert.match(accentedCard.cardProcess.work[0].accentStyle, /outline/);
  assert.equal(accentedCard.cardProcess.thread, cardValues.cardProcess.thread, 'an accent never changes the selected process');

  let changedProcess;
  const processSource = controller.state.data.operational.sources.find(source => source.kind === 'space' && source.space === process.space);
  assert.ok(processSource);
  processSource.synced_at = '2026-08-14T09:20:00Z';
  const unavailableFact = { source_kind:'space', space:process.space, code:'fixture-source-gap', summary:'Carried public-safe source summary' };
  controller.state.data.operational.unavailable.push(unavailableFact);
  for (const [freshness, label] of [['current','Source is current'], ['stale','Source is stale'], ['degraded','Source degraded']]) {
    processSource.freshness = freshness;
    changedProcess = controller.overviewValues().operationalRows.find(row => row.thread === process.thread && row.space === process.space);
    assert.equal(changedProcess.sourceResult.label, label, `${freshness} Source remains authoritative over a matching diagnostic`);
    assert.equal(changedProcess.sourceUnavailableText, '');
    assert.equal(changedProcess.sourceSynced, 'synchronized 2026-08-14T09:20:00Z', 'synced_at remains visible');
  }
  processSource.freshness = 'unavailable';
  changedProcess = controller.overviewValues().operationalRows.find(row => row.thread === process.thread && row.space === process.space);
  assert.equal(changedProcess.sourceResult.label, 'Source unavailable');
  assert.equal(changedProcess.sourceUnavailableText, 'Unavailable in this snapshot: Carried public-safe source summary.');
  assert.equal(changedProcess.sourceSynced, 'synchronized 2026-08-14T09:20:00Z', 'unavailable copy must not hide carried synced_at');
  controller.state.locale = 'ru';
  changedProcess = controller.overviewValues().operationalRows.find(row => row.thread === process.thread && row.space === process.space);
  assert.equal(changedProcess.sourceUnavailableText, 'Недоступно в этом снимке: Carried public-safe source summary.');
  controller.state.locale = 'en';
  controller.state.data.operational.sources = controller.state.data.operational.sources.filter(source => source !== processSource);
  changedProcess = controller.overviewValues().operationalRows.find(row => row.thread === process.thread && row.space === process.space);
  assert.equal(changedProcess.sourceResult.label, 'Source unavailable', 'a lone matching unavailable fact supplies the unavailable classification');
  assert.equal(changedProcess.sourceUnavailableText, 'Unavailable in this snapshot: Carried public-safe source summary.');
  controller.state.data.operational.sources.push(processSource);
  processSource.freshness = 'current';

  delete process.work[0].reported_at;
  delete process.work[0].observed_at;
  delete process.work[0].valid_until;
  for (const item of process.work) delete item.shared_pending;
  changedProcess = controller.overviewValues().operationalRows.find(row => row.thread === process.thread && row.space === process.space);
  assert.match(changedProcess.work[0].evidenceText, /reported Not recorded\./);
  assert.match(changedProcess.work[0].evidenceText, /observed Not recorded\./);
  assert.match(changedProcess.work[0].evidenceText, /valid until Not recorded\./);
  assert.equal(changedProcess.sharedPendingEmptyText, 'Not recorded.');

  process.latest_milestone.transition = '<unknown-milestone-transition>';
  changedProcess = controller.overviewValues().operationalRows.find(row => row.thread === process.thread && row.space === process.space);
  assert.equal(changedProcess.milestoneResult.label, 'Unknown value');
  assert.match(changedProcess.milestoneText, /Unknown value/);
  assert.doesNotMatch(changedProcess.milestoneText, /unknown-milestone-transition/, 'unknown raw milestone transition must never leak');
  process.work_window = { shown:2, total:2, truncated:true };
  process.consistency_window = { shown:1, total:1, truncated:true };
  controller.state.data.operational.timeline_window = { shown:1, total:1, truncated:true };
  changedProcess = controller.overviewValues().operationalRows.find(row => row.thread === process.thread && row.space === process.space);
  assert.equal(changedProcess.workWindowText, '', 'equal shown/total is not canonical truncation');
  assert.equal(changedProcess.consistencyWindowText, '', 'equal shown/total is not canonical truncation');
  assert.equal(controller.overviewValues().operationalMore, false, 'equal shown/total must not render a timeline truncation affordance');
  process.work_window = { shown:1, total:2, truncated:false };
  controller.state.data.operational.timeline_window = { shown:1, total:2, truncated:false };
  changedProcess = controller.overviewValues().operationalRows.find(row => row.thread === process.thread && row.space === process.space);
  assert.equal(changedProcess.workWindowText, '', 'shown below total is not truncation unless the carried flag is true');
  assert.equal(controller.overviewValues().operationalMore, false, 'timeline chrome also requires the carried truncation flag');

  controller.state.data.aggregates.needYou = { items:[], window:{shown:0,total:0,truncated:false} };
  values = controller.overviewValues();
  assert.deepEqual(Array.from(values.ovRows), []);
  assert.equal(values.attentionEmpty, true);
  assert.equal(values.attentionEmptyText, 'Nothing needs your move.');
  controller.state.locale = 'ru';
  assert.equal(controller.overviewValues().attentionEmptyText, 'Ничто не требует вашего хода.');
});

test('P11 Threads renders the carried reason sentence and honest transcript windows', () => {
  const { template } = runtimeDesignPage('14-local-dashboard-v4.dc.html');
  const source = readFileSync(new URL('../design-source/ThreadsView.dc.html', import.meta.url), 'utf8');
  const actorSource = readFileSync(new URL('../design-source/ActorEventLine.dc.html', import.meta.url), 'utf8');
  const timelineSource = readFileSync(new URL('../design-source/TimelineArtifactCard.dc.html', import.meta.url), 'utf8');
  assert.match(template, /data-thread-card[\s\S]*data-thread-title[\s\S]*data-thread-lead[\s\S]*\{\{ whoseMoveText \}\}[\s\S]*data-thread-protocol[\s\S]*data-thread-participants[\s\S]*data-thread-membership[\s\S]*data-thread-open-items[\s\S]*\{\{ o\.reasonSentence \}\}[\s\S]*data-thread-transcript[\s\S]*\{\{ transcriptWindowText \}\}[\s\S]*data-thread-snapshot/);
  assert.match(template, /data-thread-technical[\s\S]*\{\{ o\.technicalWhy \}\}[\s\S]*\{\{ o\.ruleIdentity \}\}/);
  assert.doesNotMatch(source, /\bSTATE_TONE\b|\bHUMAN_GATE_TEXT\b|statuses\s*:/);
  assert.doesNotMatch(source, /\.sort\s*\(|tvEntryTotal|tvHiddenReportPublishes|tvMissingReportCount|tvRepresentedWorkIDs/);
  assert.doesNotMatch(source, /due\s*<\s*at|already passed|уже прош[её]л/);
  assert.doesNotMatch(source, /tv\.settled\s*\?\s*"settled"\s*:\s*"open"|first\s*\?\s*first\.outcome|tvProtocolResult|tvPendingSignalTone|tvSettledSignalLabel/);
  assert.doesNotMatch(source, /function relationsOf|a\.parent|a\.refs/);
  assert.doesNotMatch(source, /<dc-import name="EventSignal"/);
  assert.match(template, /data-thread-links[\s\S]*\{\{ l\.fromTitle \}\}[\s\S]*\{\{ l\.toTitle \}\}[\s\S]*data-thread-diagnostics[\s\S]*\{\{ diag\.severityResult \}\}[\s\S]*<details data-thread-technical/);
  assert.match(source, /A2A_VOCABULARY_RESOLVER\.lookup/);
  assert.match(actorSource, /transitionResult[\s\S]*transitionResult\.label/);
  assert.match(actorSource, /data-event-note[\s\S]*\{\{ noteText \}\}/);
  assert.match(template, /<dc-import name="FreshnessDot" result="\{\{ tvFreshnessResult \}\}" age="\{\{ tvFreshnessAge \}\}" compact="\{\{ true \}\}"/);
  assert.doesNotMatch(template, /<dc-import name="FreshnessDot"[^>]*(?:stale|hint)=/);
  assert.match(timelineSource, /<dc-import name="StatusPill" result="\{\{ stateResult \}\}"/);

  const controller = dashboardController({ fetch: async () => ({ ok: false }), EventSource: null });
  controller.state.data = JSON.parse(readFileSync(new URL('../../internal/html/demo-data.json', import.meta.url), 'utf8'));
  controller.state.view = 'threads';
  const thread = controller.state.data.threads[0];
  const view = controller.state.data.threadViews.find(candidate => candidate.space === thread.space && candidate.thread === thread.id);
  assert.ok(view);
  controller.state.threadSel = thread.id;
  controller.state.threadSpace = thread.space;

  const sharedOverviewItem = controller.state.data.inbox[0];
  sharedOverviewItem.reasonSentence = {
    en: 'Atlas must approve this decision; G3 requires a person.',
    ru: 'Решение должен согласовать Atlas; по правилу G3 нужен человек.'
  };
  const lead = view.open_items[0];
  lead.reasonSentence = sharedOverviewItem.reasonSentence;
  lead.why = 'specs/03-domain.md: exact technical rule evidence';
  lead.ruleIdentity = { family:'decision', state:'proposed', transition:'approve' };
  lead.human_gate = 'G3';
  lead.expected_transition = 'approve';
  lead.state = 'proposed';
  lead.outcome = 'open';

  const [firstArtifact, secondArtifact] = view.artifacts;
  const transcriptEvent = view.transcript.find(row => row.kind === 'event');
  assert.ok(transcriptEvent);
  transcriptEvent.event.transition = 'submit';
  view.transcript = [
    { kind:'artifact', at:'2026-08-14T08:00:00Z', artifact:firstArtifact },
    transcriptEvent,
    { kind:'artifact', at:'2026-08-14T09:00:00Z', artifact:secondArtifact }
  ];
  view.windows.transcript = { shown:3, total:19, truncated:true };
  view.windows.openItems = { shown:1, total:4, truncated:true };
  view.windows.flags = { shown:1, total:3, truncated:true };
  view.windows.unresolved = { shown:1, total:2, truncated:true };
  view.windows.deliveries = { shown:1, total:2, truncated:true };
  const carriedLink = { from:firstArtifact.id, to:'missing-link-target', kind:'ref' };
  thread.links = [carriedLink];
  thread.windows.links = { shown:1, total:5, truncated:true };
  view.deliveries = [{ handoffId:firstArtifact.id, resolution:'unresolved', ref:'DP-fixture', name:'Fixture delivery', unavailable:'raw resolver diagnostic' }];
  view.unresolved = [{ id:'missing-link-target', kind:'ref' }];
  view.flags = [{ kind:'fixture-consistency', subject:firstArtifact.id, event_ulid:transcriptEvent.event.ulid, consistency:{ actual:'responded', claimed:'in_progress', cause:'raw diagnostic cause' } }];
  const operationalThread = controller.state.data.operational.timeline.find(row => row.space === view.space && row.thread === view.thread);
  assert.ok(operationalThread);
  operationalThread.consistency = [{ code:'fixture-consistency', subject_ref:firstArtifact.id, severity:'warning', summary:'Carried consistency summary' }];
  view.sync_stale = false;
  const threadSource = controller.state.data.operational.sources.find(source => source.kind === 'space' && source.space === view.space);
  assert.ok(threadSource);
  threadSource.freshness = 'degraded';
  controller.state.data.operational.unavailable.push({ source_kind:'space', space:view.space, code:'fixture-degraded', summary:'Carried thread source evidence is incomplete' });
  controller.state.data.workReports = [{
    space:view.space, thread:view.thread, artifact_id:'XA-must-not-be-inserted',
    reported_at:'2026-08-14T10:00:00Z', commit_sequence:999
  }];

  let values = controller.threadsValues();
  assert.equal(values.whoseMoveText, sharedOverviewItem.reasonSentence.en, 'thread and Overview consume the exact same carried string');
  assert.equal(values.tvOpen[0].reasonSentence, sharedOverviewItem.reasonSentence.en);
  assert.equal(values.tvOpen[0].technicalWhy, lead.why);
  assert.equal(values.tvOpen[0].technicalExpectedTransition, 'expected_transition=approve');
  assert.match(values.tvOpen[0].ruleIdentity, /approve/);
  assert.equal(values.tvOpen[0].stateResult.label, 'Proposed for review');
  assert.equal(values.tvOpen[0].outcomeResult.label, 'Still open');
  assert.equal(values.tvOpen[0].gateResult.label, 'Owner approval gate');
  assert.deepEqual(Array.from(values.tvOpen, row => row.id), Array.from(view.open_items, item => item.id), 'open items preserve carried order');
  assert.deepEqual(Array.from(values.tvEntries, row => row.id), [firstArtifact.id, transcriptEvent.event.ulid, secondArtifact.id], 'transcript preserves carried order and ignores global reports');
  const eventRow = values.tvEntries[1];
  assert.equal(eventRow.transitionResult.label, 'submitted for review');
  assert.equal(eventRow.actionText, eventRow.transitionResult.label);
  transcriptEvent.event.transition = 'note';
  transcriptEvent.event.note = 'A carried note body that must stay in the transcript.';
  values = controller.threadsValues();
  assert.equal(values.tvEntries[1].hasNoteText, true);
  assert.equal(values.tvEntries[1].noteText, transcriptEvent.event.note);
  transcriptEvent.event.transition = 'submit';
  delete transcriptEvent.event.note;
  values = controller.threadsValues();
  assert.equal(values.transcriptWindowVisible, true);
  assert.equal(values.transcriptWindowText, 'Showing 3 of 19.');
  assert.equal(values.openItemsWindowText, 'Showing 1 of 4.');
  assert.equal(values.linksWindowText, 'Showing 1 of 5.');
  assert.equal(values.flagsWindowText, 'Showing 1 of 3.');
  assert.equal(values.unresolvedWindowText, 'Showing 1 of 2.');
  assert.equal(values.deliveriesWindowText, 'Showing 1 of 2.');
  assert.deepEqual(Array.from(values.tvLinks, link => [link.from, link.to, link.kind]), [[carriedLink.from, carriedLink.to, carriedLink.kind]]);
  assert.equal(values.tvLinks[0].toTitle, 'Not recorded.');
  assert.equal(values.tvDiagnostics[0].severityResult.label, 'Sources need review');
  assert.match(values.tvDiagnostics[0].technical, /raw diagnostic cause/);
  assert.equal(values.tvFreshnessResult.label, 'Source degraded');
  assert.equal(values.tvSourceUnavailableEvidence, 'Carried thread source evidence is incomplete');

  threadSource.freshness = 'unavailable';
  values = controller.threadsValues();
  assert.equal(values.tvFreshnessResult.label, 'Source unavailable');
  assert.equal(values.tvSnapshotDegradedText, 'Unavailable in this snapshot: Carried thread source evidence is incomplete.');

  controller.state.locale = 'ru';
  values = controller.threadsValues();
  assert.equal(values.whoseMoveText, sharedOverviewItem.reasonSentence.ru);
  assert.equal(values.transcriptWindowText, 'Показано 3 из 19.');
  assert.equal(values.tvEntries[1].transitionResult.label, 'передано на рассмотрение');
  assert.equal(values.tvFreshnessResult.label, 'Источник недоступен');
  assert.equal(values.tvSnapshotDegradedText, 'Недоступно в этом снимке: Carried thread source evidence is incomplete.');
  controller.state.locale = 'en';

  const unavailableFacts = controller.state.data.operational.unavailable;
  controller.state.data.operational.unavailable = unavailableFacts.filter(fact => fact.space !== view.space);
  values = controller.threadsValues();
  assert.equal(values.tvSnapshotDegradedText, 'Unknown in this snapshot.');
  controller.state.data.operational.unavailable = unavailableFacts;

  view.windows.transcript = { shown:3, total:3, truncated:true };
  values = controller.threadsValues();
  assert.equal(values.transcriptWindowVisible, false);
  assert.equal(values.transcriptWindowText, '');

  transcriptEvent.event.transition = '<untrusted-transition>';
  values = controller.threadsValues();
  assert.equal(values.tvEntries[1].transitionResult.label, 'Unknown value');
  assert.equal(values.tvEntries[1].actionText, 'Unknown value');
  assert.doesNotMatch(values.tvEntries[1].actionText, /untrusted-transition/);

  const savedOpenItems = view.open_items;
  const savedTranscript = view.transcript;
  view.open_items = [];
  view.transcript = [];
  view.windows.openItems = { shown:0, total:0, truncated:false };
  view.windows.transcript = { shown:0, total:0, truncated:false };
  values = controller.threadsValues();
  assert.equal(values.tvOpenKnownEmpty, true);
  assert.equal(values.tvTranscriptKnownEmpty, true);
  assert.equal(values.noneRecordedText, 'None recorded.');
  view.open_items = savedOpenItems;
  view.transcript = savedTranscript;

  const savedTranscriptWindow = view.windows.transcript;
  const savedDeliveriesWindow = view.windows.deliveries;
  const savedLinksWindow = thread.windows.links;
  delete view.windows.transcript;
  delete view.windows.deliveries;
  delete thread.windows.links;
  values = controller.threadsValues();
  assert.equal(values.tvCardTranscriptText, 'Transcript and relationships: Unknown in this snapshot.', 'missing carried windows must not turn visible prefix lengths into totals');
  view.windows.transcript = savedTranscriptWindow;
  view.windows.deliveries = savedDeliveriesWindow;
  thread.windows.links = savedLinksWindow;

  const savedViewTitle = view.opener.title;
  const savedThreadTitle = thread.opener.title;
  view.opener.title = '';
  thread.opener.title = '';
  values = controller.threadsValues();
  assert.equal(values.tvTitle, 'Not recorded.');
  assert.match(values.tvMembershipText, /^Not recorded\./);
  view.opener.title = savedViewTitle;
  thread.opener.title = savedThreadTitle;

  const threadID = controller.cardID('thread', { space:view.space, thread:view.thread });
  values.threadRows.find(row => row.id === view.thread && row.space === view.space).openCard();
  assert.deepEqual(JSON.parse(JSON.stringify(controller.state.card)), { kind:'thread', id:threadID, accent:'' });
  const cardValues = controller.threadsValues({ card:{ kind:'thread', id:threadID, accent:'' } });
  assert.equal(cardValues.cardMode, true);
  assert.equal(cardValues.tvTitle, view.opener.title);
  const baseline = {
    open:Array.from(cardValues.tvOpen, row => row.id),
    transcript:Array.from(cardValues.tvEntries, row => row.id),
    links:Array.from(cardValues.tvLinks, row => [row.from, row.to, row.kind])
  };
  const accentChecks = [
    ['member', firstArtifact.id, values => values.tvEntries[0].accentStyle],
    ['open-item', lead.id, values => values.tvOpen[0].accentStyle],
    ['participant', view.participants[0], values => values.tvParticipantFaces[0].accentStyle],
    ['event', transcriptEvent.event.ulid, values => values.tvEntries[1].accentStyle],
    ['reference', [carriedLink.from, carriedLink.to, carriedLink.kind], values => values.tvLinks[0].accentStyle],
    ['delivery', [firstArtifact.id, 'DP-fixture'], values => values.tvEntries[0].deliveries[0].accentStyle]
  ];
  for (const [family, identity, highlighted] of accentChecks) {
    const accented = controller.threadsValues({ card:{ kind:'thread', id:threadID, accent:controller.cardAccent(family, identity) } });
    assert.match(highlighted(accented), /outline/, `${family} accent must highlight its carried target`);
    assert.deepEqual({
      open:Array.from(accented.tvOpen, row => row.id),
      transcript:Array.from(accented.tvEntries, row => row.id),
      links:Array.from(accented.tvLinks, row => [row.from, row.to, row.kind])
    }, baseline, `${family} accent must not change content or order`);
  }
});

test('contract version selection switches one atomic package without cross-version facts', () => {
  const controller = dashboardController({ fetch: async () => ({ ok: false }), EventSource: null });
  controller.state.data = JSON.parse(readFileSync(new URL('../../internal/html/testdata/demo.json', import.meta.url), 'utf8'));
  controller.state.view = 'contracts';
  controller.state.conSel = 'XC-atlas-order-envelope';
  const contract = controller.state.data.contracts.find(candidate => candidate.id === controller.state.conSel);
  const versionFixture = version => contract.versions.find(candidate => candidate.version === version);

  let values = controller.renderVals();
  assert.equal(values.cdIsOverview, true);
  assert.equal(values.cdIsVersion, false);

  const currentVersionCard = values.cdVersions.find(version => version.version === '2.2.0');
  assert.equal(currentVersionCard.role, 'button');
  currentVersionCard.openVersion();
  assert.equal(controller.state.card.kind, 'cver');

  values = controller.contractsValues({ card:controller.state.card });
  assert.equal(values.cardContractVersion, true);
  assert.deepEqual(Array.from(values.contractVersionCard.consumerPins, pin => pin.system), ['checkout']);
  const assertExactPackage = (card, version) => {
    const expected = Array.from(versionFixture(version).detail.documents, document => document.path);
    const rows = Array.from(card.documents);
    assert.deepEqual(Array.from(rows, row => row.path), expected, `${version}: document order and membership come from one carried version package`);
    assert.ok(rows.every(row => row.path.includes(`/XC-atlas-order-envelope/${version}/`)), `${version}: no sibling-version document leaks into the package`);
  };
  assertExactPackage(values.contractVersionCard, '2.2.0');
  assert.equal(values.contractVersionCard.provenance.commitSHA, '22aa11aa11aa11aa11aa11aa11aa11aa11aa11aa');
  assert.notEqual(values.contractVersionCard.provenance.commitSHA, versionFixture('2.1.0').detail.provenance.commitSHA);

  controller.closeCard();
  values = controller.renderVals();
  const retiredVersionCard = values.cdVersions.find(version => version.version === '1.5.0');
  assert.equal(retiredVersionCard.role, 'button');
  retiredVersionCard.openVersion();
  values = controller.contractsValues({ card:controller.state.card });
  assert.equal(values.contractVersionCard.provenance.profile, 'contract-tree-v1');
  assert.deepEqual(Array.from(values.contractVersionCard.consumerPins, pin => pin.system), ['fulfillment']);
  assertExactPackage(values.contractVersionCard, '1.5.0');
  assert.equal(values.contractVersionCard.provenance.commitSHA, '15aa11aa11aa11aa11aa11aa11aa11aa11aa11aa');
  assert.notEqual(values.contractVersionCard.provenance.commitSHA, versionFixture('2.2.0').detail.provenance.commitSHA);

  values.contractVersionCard.openSuccessor();
  values = controller.contractsValues({ card:controller.state.card });
  assert.equal(values.contractVersionCard.title, 'XC-atlas-order-envelope 2.0.0');
  assertExactPackage(values.contractVersionCard, '2.0.0');
});

test('the live dashboard applies only the newest conditional snapshot and preserves attention data', async () => {
  const gets = [];
  const pending = [];
  class FakeEventSource {
    static instance;
    constructor(url) { this.url = url; this.handlers = {}; FakeEventSource.instance = this; }
    addEventListener(name, handler) { this.handlers[name] = handler; }
    removeEventListener(name) { delete this.handlers[name]; }
    close() { this.closed = true; }
  }
  const oldDigest = digest('a');
  const secondDigest = digest('b');
  const thirdDigest = digest('c');
  const controller = dashboardController({
    EventSource: FakeEventSource,
    fetch: (url, options = {}) => {
      if (options.method === 'HEAD') return Promise.resolve({ ok: true, headers: responseHeaders(etag(oldDigest)) });
      gets.push({ url, options });
      return new Promise(resolve => pending.push(resolve));
    }
  });
  const attention = [{ id: 'XQ-attention', reasons: ['gate-pending-on-me'], prompt: { text: 'review' } }];
  const initial = dashboardData();
  initial.inbox = attention;
  initial.operational.revision = digest('0');
  controller.state.data = initial;
  controller.startOperationalStream(initial);
  await nextTurn();

  const source = FakeEventSource.instance;
  assert.equal(source.url, '/api/v1/events');
  if (source.handlers.open) source.handlers.open();
  source.handlers.revision({ data: JSON.stringify({ revision: digest('1'), viewModel: secondDigest }), lastEventId: secondDigest });
  source.handlers.revision({ data: JSON.stringify({ revision: digest('2'), viewModel: thirdDigest }), lastEventId: thirdDigest });
  await nextTurn();
  assert.equal(gets.length, 0, 'SSE invalidation must not fetch or adopt content');
  controller.state.refresh();
  controller.state.refresh();
  assert.equal(gets.length, 2);
  assert.equal(gets[0].url, '/api/v1/dashboard');
  assert.equal(gets[0].options.headers['If-None-Match'], etag(oldDigest));

  const newest = structuredClone(initial);
  newest.operational.revision = digest('2');
  newest.operational.timeline = [{ thread: 'thread:three' }];
  pending[1]({ ok: true, status: 200, headers: responseHeaders(etag(thirdDigest)), json: async () => newest });
  await nextTurn();
  await nextTurn();
  const older = structuredClone(initial);
  older.operational.revision = digest('1');
  older.operational.timeline = [{ thread: 'thread:two' }];
  pending[0]({ ok: true, status: 200, headers: responseHeaders(etag(secondDigest)), json: async () => older });
  await nextTurn();
  await nextTurn();

  assert.equal(controller.state.data.operational.revision, digest('2'));
  assert.equal(controller.state.data.operational.timeline[0].thread, 'thread:three');
  assert.deepEqual(controller.state.data.inbox, attention);
  assert.equal(controller.state.liveTransport, 'updated');
  controller.componentWillUnmount();
  assert.equal(source.closed, true);
  assert.equal(controller.state.refresh, null);
});

test('a failed newest pull does not suppress an older successful snapshot', async () => {
  const pending = [];
  class FakeEventSource {
    static instance;
    constructor() { this.handlers = {}; FakeEventSource.instance = this; }
    addEventListener(name, handler) { this.handlers[name] = handler; }
    removeEventListener(name) { delete this.handlers[name]; }
    close() { this.closed = true; }
  }
  const oldDigest = digest('d');
  const secondDigest = digest('e');
  const controller = dashboardController({
    EventSource: FakeEventSource,
    fetch: (_url, options = {}) => {
      if (options.method === 'HEAD') return Promise.resolve({ ok: true, headers: responseHeaders(etag(oldDigest)) });
      return new Promise(resolve => pending.push(resolve));
    }
  });
  const initial = dashboardData();
  initial.operational.revision = digest('0');
  controller.state.data = initial;
  controller.startOperationalStream(initial);
  await nextTurn();

  const source = FakeEventSource.instance;
  source.handlers.open();
  controller.state.refresh();
  controller.state.refresh();
  pending[1]({ ok: false, status: 500 });
  await nextTurn();
  assert.equal(controller.state.liveTransport, 'stale');
  const older = structuredClone(initial);
  older.operational.revision = digest('1');
  older.operational.timeline = [{ thread: 'thread:two' }];
  pending[0]({ ok: true, status: 200, headers: responseHeaders(etag(secondDigest)), json: async () => older });
  await nextTurn();
  await nextTurn();

  assert.equal(controller.state.data.operational.revision, digest('1'));
  assert.equal(controller.state.data.operational.timeline[0].thread, 'thread:two');
  controller.componentWillUnmount();
});

test('live invalidation tracks revision and digest without adopting dashboard content', async () => {
  const requests = [];
  const emitted = [];
  class FakeEventSource {
    static instance;
    constructor(url) { this.url = url; this.handlers = {}; FakeEventSource.instance = this; }
    addEventListener(name, handler) { this.handlers[name] = handler; }
    removeEventListener(name) { delete this.handlers[name]; }
    close() { this.closed = true; }
  }
  const acceptedDigest = digest('1');
  const changedDigest = digest('2');
  const initial = dashboardData();
  initial.operational.revision = digest('3');
  const before = structuredClone(initial);
  const controller = dashboardController({
    EventSource: FakeEventSource,
    fetch: async (url, options) => {
      requests.push([url, options]);
      return { ok:true, status:200, headers:responseHeaders(etag(acceptedDigest)) };
    }
  });
  controller.state.data = initial;
  controller.startOperationalStream(initial);
  controller.liveController.props.onState = value => { emitted.push(value); controller.setState({ liveTransport:value }); };
  await nextTurn();
  FakeEventSource.instance.handlers.open();
  FakeEventSource.instance.handlers.revision({ data:JSON.stringify({ revision:digest('3'), viewModel:acceptedDigest }), lastEventId:acceptedDigest });
  assert.equal(emitted.length, 0, 'the accepted revision/digest pair is a no-op');
  FakeEventSource.instance.handlers.revision({ data:JSON.stringify({ revision:digest('3'), viewModel:changedDigest }), lastEventId:changedDigest });
  assert.equal(controller.state.liveTransport, 'newer-available');
  assert.deepEqual(controller.state.data, before);
  assert.deepEqual(requests.map(([url, options]) => [url, options.method]), [['/api/v1/dashboard','HEAD']]);
  const emittedCount = emitted.length;
  FakeEventSource.instance.handlers.revision({ data:JSON.stringify({ revision:digest('3'), viewModel:changedDigest }), lastEventId:changedDigest });
  FakeEventSource.instance.handlers.revision({ data:JSON.stringify({ revision:digest('3') }), lastEventId:changedDigest });
  FakeEventSource.instance.handlers.revision({ data:'{', lastEventId:changedDigest });
  assert.equal(emitted.length, emittedCount, 'replayed and malformed events are inert');
  const liveSource = readFileSync(new URL('../design-source/DashboardLive.dc.html', import.meta.url), 'utf8');
  assert.doesNotMatch(liveSource, /\/api\/v1\/snapshot|retryTimers/);
  assert.doesNotMatch(liveSource, /initialRevision|onSnapshot/);
  assert.match(liveSource, /&quot;data&quot;:[\s\S]*&quot;onHydrate&quot;:[\s\S]*&quot;onState&quot;:[\s\S]*&quot;onReady&quot;:/);
  controller.componentWillUnmount();
});

test('one root hydrate path owns embedded and accepted complete dashboard adoption', async () => {
  const initial = dashboardData();
  initial.operational.revision = digest('4');
  const refreshed = structuredClone(initial);
  refreshed.operational.revision = digest('5');
  const acceptedDigest = digest('3');
  const refreshedDigest = digest('4');
  const responses = [];
  class FakeEventSource {
    static instance;
    constructor() { this.handlers = {}; FakeEventSource.instance = this; }
    addEventListener(name, handler) { this.handlers[name] = handler; }
    removeEventListener(name) { delete this.handlers[name]; }
    close() { this.closed = true; }
  }
  const controller = dashboardController({
    demo: initial,
    EventSource: FakeEventSource,
    fetch: async (_url, options) => {
      if (options.method === 'HEAD') return { ok:true, headers:responseHeaders(etag(acceptedDigest)) };
      return responses.shift();
    }
  });
  const originalHydrate = controller.hydrate.bind(controller);
  const hydrated = [];
  controller.hydrate = candidate => { hydrated.push(candidate); return originalHydrate(candidate); };
  controller.componentDidMount();
  assert.equal(hydrated.length, 1, 'embedded data must enter through hydrate');
  controller.startOperationalStream(controller.state.data);
  await nextTurn();
  FakeEventSource.instance.handlers.open();
  responses.push({ ok:true, status:200, headers:responseHeaders(etag(refreshedDigest)), json:async () => refreshed });
  await controller.state.refresh();
  assert.equal(hydrated.length, 2, 'an accepted 200 must call hydrate exactly once');
  responses.push({ ok:false, status:304, headers:responseHeaders(etag(refreshedDigest)), json:async () => { throw new Error('304 body parsed'); } });
  await controller.state.refresh();
  responses.push({ ok:false, status:500, headers:responseHeaders(null), json:async () => { throw new Error('500 body parsed'); } });
  await controller.state.refresh();
  responses.push({ ok:true, status:200, headers:responseHeaders(etag(digest('5'))), json:async () => { throw new SyntaxError('empty body'); } });
  await controller.state.refresh();
  responses.push({ ok:true, status:200, headers:responseHeaders(etag(digest('6'))), json:async () => ({ operational:{ revision:'revision:truncated' } }) });
  await controller.state.refresh();
  const partial = {
    meta:structuredClone(refreshed.meta),
    operational:{ ...structuredClone(refreshed.operational), revision:digest('7') },
    vocabulary:structuredClone(refreshed.vocabulary),
  };
  const lastGood = structuredClone(controller.state.data);
  responses.push({ ok:true, status:200, headers:responseHeaders(etag(digest('7'))), json:async () => partial });
  await controller.state.refresh();
  const nestedPartial = structuredClone(refreshed);
  nestedPartial.operational.revision = digest('8');
  delete nestedPartial.threadViews[0].windows.transcript;
  responses.push({ ok:true, status:200, headers:responseHeaders(etag(digest('8'))), json:async () => nestedPartial });
  await controller.state.refresh();
  responses.push({ ok:true, status:200, headers:responseHeaders(null), json:async () => refreshed });
  await controller.state.refresh();
  assert.equal(hydrated.length, 2, '304, failures, missing ETag, empty and structurally partial bodies must never hydrate');
  assert.equal(JSON.stringify(controller.state.data), JSON.stringify(lastGood), 'a valid-digest partial candidate must preserve the complete last-good model');
  assert.equal(originalHydrate(partial), false, 'the one root hydrate door must independently reject a structurally partial model');
  assert.equal(JSON.stringify(controller.state.data), JSON.stringify(lastGood), 'direct rejection must not normalize missing collections into an adopted empty dashboard');
  controller.componentWillUnmount();
});

test('a successful 304 advances ordering without parsing or hydrating', async () => {
  const initial = dashboardData();
  initial.operational.revision = digest('6');
  const acceptedDigest = digest('0');
  const pending = [];
  class FakeEventSource {
    static instance;
    constructor() { this.handlers = {}; FakeEventSource.instance = this; }
    addEventListener(name, handler) { this.handlers[name] = handler; }
    removeEventListener(name) { delete this.handlers[name]; }
    close() { this.closed = true; }
  }
  const controller = dashboardController({
    EventSource:FakeEventSource,
    fetch:(_url, options) => options.method === 'HEAD'
      ? Promise.resolve({ ok:true, headers:responseHeaders(etag(acceptedDigest)) })
      : new Promise(resolve => pending.push(resolve))
  });
  controller.state.data = initial;
  controller.startOperationalStream(initial);
  await nextTurn();
  FakeEventSource.instance.handlers.open();
  let hydrateCalls = 0;
  const originalHydrate = controller.hydrate.bind(controller);
  controller.hydrate = candidate => { hydrateCalls += 1; return originalHydrate(candidate); };
  const older = controller.state.refresh();
  const newer = controller.state.refresh();
  pending[1]({ ok:false, status:304, headers:responseHeaders(etag(acceptedDigest)), json:async () => { throw new Error('304 parsed'); } });
  await newer;
  const staleCandidate = structuredClone(initial);
  staleCandidate.operational.revision = digest('7');
  pending[0]({ ok:true, status:200, headers:responseHeaders(etag(digest('f'))), json:async () => staleCandidate });
  await older;
  assert.equal(hydrateCalls, 0);
  assert.strictEqual(controller.state.data, initial);
  assert.equal(controller.state.liveTransport, 'updated');
  controller.componentWillUnmount();
});

test('hydrate preserves every pre-P15 UI field and retains a gone opaque card context', () => {
  const initial = dashboardData();
  initial.operational.revision = digest('8');
  const focus = { id:'reading-focus' };
  const controller = dashboardController({ fetch:async () => ({ ok:false }), EventSource:null, hash:'#reading-position', focus });
  controller.state.data = initial;
  const selected = initial.inbox[0];
  const card = { kind:'item', id:controller.cardID('item', selected), accent:controller.cardAccent('state', selected.state) };
  Object.keys(controller.state).forEach(key => {
    if (key !== 'data' && key !== 'error' && typeof controller.state[key] === 'string') controller.state[key] = `preserved:${key}`;
  });
  controller.state.card = card;
  controller.state.locale = 'en';
  const refresh = () => {};
  controller.state.refresh = refresh;
  const rootLogic = runtimeDesignPage('14-local-dashboard-v4.dc.html').logic;
  const uiFields = JSON.parse(rootLogic.match(/const DASHBOARD_UI_FIELDS = (\{[\s\S]*?\n\});/)?.[1] || '{}');
  const registeredUIFields = new Set(Object.values(uiFields).flat());
  const beforeUI = Object.fromEntries(Object.entries(controller.state).filter(([key]) => !['data','retainedCardData','goneCard','liveTransport','refresh'].includes(key)));
  const candidate = structuredClone(initial);
  candidate.operational.revision = digest('9');
  for (const key of ['inbox','outbox','archive']) candidate[key] = candidate[key].filter(item => controller.cardID('item', item) !== card.id);
  const hash = controller.testLocation.hash;
  const originalGo = controller.go;
  const originalCloseCard = controller.closeCard;
  let navigationCalls = 0;
  let closeCalls = 0;
  controller.go = () => { navigationCalls += 1; };
  controller.closeCard = () => { closeCalls += 1; };
  controller.testContext.location.reload = () => { throw new Error('hydrate reloaded the page'); };
  assert.equal(controller.hydrate(candidate), true);
  assert.deepEqual(Object.fromEntries(Object.entries(controller.state).filter(([key]) => Object.hasOwn(beforeUI, key))), beforeUI);
  for (const key of [...registeredUIFields].filter(key => key !== 'liveTransport')) assert.deepEqual(controller.state[key], beforeUI[key], `${key} from DASHBOARD_UI_FIELDS must survive hydrate`);
  assert.strictEqual(controller.state.retainedCardData, initial);
  assert.equal(controller.state.goneCard, true);
  assert.deepEqual(controller.state.card, card);
  assert.equal(controller.testLocation.hash, hash);
  assert.strictEqual(controller.state.refresh, refresh);
  assert.equal(controller.state.liveTransport, 'preserved:liveTransport');
  assert.strictEqual(controller.testContext.document.activeElement, focus);
  assert.equal(navigationCalls, 0);
  assert.equal(closeCalls, 0);
  const values = controller.renderVals();
  assert.strictEqual(values.modalCtx.data, controller.state.data, 'Modal keeps the accepted dashboard as its ordinary context');
  assert.equal(values.modalCtx.ui.goneCard, true);
  assert.strictEqual(values.modalCtx.ui.retainedCardData, initial, 'only Modal receives retained old-card data');
  controller.go = originalGo;
  controller.closeCard = originalCloseCard;
  controller.openCard('space', controller.cardID('space', candidate.spaces[0]), '');
  assert.equal(controller.state.goneCard, false);
  assert.equal(controller.state.retainedCardData, null);
  controller.closeCard();
  assert.equal(controller.state.goneCard, false);
});

test('hydrate keeps surviving card tuple and filters when parents or accent targets shrink', () => {
  const initial = dashboardData();
  const controller = dashboardController({ fetch:async () => ({ ok:false }), EventSource:null, hash:'#unchanged' });
  controller.state.data = initial;
  const selected = initial.inbox[0];
  controller.state.card = { kind:'item', id:controller.cardID('item', selected), accent:controller.cardAccent('state', selected.state) };
  controller.state.view = 'work';
  controller.state.workTab = 'incoming';
  controller.state.workState = 'open';
  const before = { card:structuredClone(controller.state.card), view:controller.state.view, workTab:controller.state.workTab, workState:controller.state.workState, hash:controller.testLocation.hash };
  const candidate = structuredClone(initial);
  candidate.inbox = candidate.inbox.filter(item => controller.cardID('item', item) === controller.state.card.id);
  candidate.inbox[0].state = 'changed-accent-target';
  candidate.operational.revision = digest('a');
  assert.equal(controller.hydrate(candidate), true);
  assert.deepEqual({ card:controller.state.card, view:controller.state.view, workTab:controller.state.workTab, workState:controller.state.workState, hash:controller.testLocation.hash }, before);
  assert.equal(controller.state.goneCard, false);
  assert.equal(controller.state.retainedCardData, null);
});

test('hydrate retains selection when its space is gone or both card and space disappear', () => {
  const cases = [
    {
      name:'space gone',
      select(controller, data) { const source = data.spaces[0]; return { kind:'space', id:controller.cardID('space', source), accent:controller.cardAccent('source', source.id) }; },
      remove(controller, data, selection) { data.spaces = data.spaces.filter(space => controller.cardID('space', space) !== selection.id); }
    },
    {
      name:'card and space gone',
      select(controller, data) { const source = data.inbox[0]; return { kind:'item', id:controller.cardID('item', source), accent:controller.cardAccent('state', source.state), space:source.space }; },
      remove(controller, data, selection) {
        for (const key of ['inbox','outbox','archive']) data[key] = data[key].filter(item => controller.cardID('item', item) !== selection.id);
        data.spaces = data.spaces.filter(space => space.id !== selection.space);
      }
    }
  ];
  for (const [scenarioIndex, scenario] of cases.entries()) {
    const initial = dashboardData();
    const controller = dashboardController({ fetch:async () => ({ ok:false }), EventSource:null });
    controller.state.data = initial;
    const selection = scenario.select(controller, initial);
    controller.state.card = { kind:selection.kind, id:selection.id, accent:selection.accent };
    const candidate = structuredClone(initial);
    scenario.remove(controller, candidate, selection);
    candidate.operational.revision = digest(scenarioIndex ? 'c' : 'b');
    assert.equal(controller.hydrate(candidate), true, scenario.name);
    assert.equal(controller.state.goneCard, true, scenario.name);
    assert.strictEqual(controller.state.retainedCardData, initial, scenario.name);
    assert.deepEqual(controller.state.card, { kind:selection.kind, id:selection.id, accent:selection.accent }, scenario.name);
  }
});

test('failed refreshes retain last-good data and carried source freshness exactly', async () => {
  const initial = dashboardData();
  initial.operational.revision = digest('d');
  const before = structuredClone(initial);
  const acceptedDigest = digest('7');
  const responses = [
    () => Promise.reject(new Error('offline')),
    () => Promise.resolve({ ok:false, status:503, headers:responseHeaders(null) }),
    () => Promise.resolve({ ok:true, status:200, headers:responseHeaders(etag(digest('8'))), json:async () => { throw new SyntaxError('malformed'); } }),
    () => Promise.resolve({ ok:true, status:200, headers:responseHeaders(etag(digest('9'))), json:async () => { throw new SyntaxError('empty'); } })
  ];
  class FakeEventSource {
    static instance;
    constructor() { this.handlers = {}; FakeEventSource.instance = this; }
    addEventListener(name, handler) { this.handlers[name] = handler; }
    removeEventListener(name) { delete this.handlers[name]; }
    close() { this.closed = true; }
  }
  const controller = dashboardController({ EventSource:FakeEventSource, fetch:(_url, options) => options.method === 'HEAD' ? Promise.resolve({ ok:true, headers:responseHeaders(etag(acceptedDigest)) }) : responses.shift()() });
  controller.state.data = initial;
  controller.startOperationalStream(initial);
  await nextTurn();
  FakeEventSource.instance.handlers.open();
  for (let index = 0; index < 4; index += 1) {
    await controller.state.refresh();
    assert.equal(controller.state.liveTransport, 'stale');
    assert.deepEqual(controller.state.data, before);
    assert.deepEqual(controller.state.data.operational.sources, before.operational.sources);
  }
  controller.componentWillUnmount();
});

test('live transport scenarios cover exactly the carried vocabulary denominator', async () => {
  const initial = dashboardData();
  initial.operational.revision = digest('e');
  const acceptedDigest = digest('a');
  const nextDigest = digest('b');
  const states = new Set();
  let resolveRefresh;
  class FakeEventSource {
    static instance;
    constructor() { this.handlers = {}; FakeEventSource.instance = this; }
    addEventListener(name, handler) { this.handlers[name] = handler; }
    removeEventListener(name) { delete this.handlers[name]; }
    close() { this.closed = true; }
  }
  const controller = dashboardController({
    EventSource:FakeEventSource,
    fetch:(_url, options) => options.method === 'HEAD'
      ? Promise.resolve({ ok:true, headers:responseHeaders(etag(acceptedDigest)) })
      : new Promise(resolve => { resolveRefresh = resolve; })
  });
  controller.state.data = initial;
  controller.startOperationalStream(initial);
  controller.liveController.props.onState = value => { if (value) states.add(value); controller.setState({ liveTransport:value }); };
  await nextTurn();
  FakeEventSource.instance.handlers.open();
  FakeEventSource.instance.handlers.revision({ data:JSON.stringify({ revision:digest('f'), viewModel:nextDigest }), lastEventId:nextDigest });
  const pendingRefresh = controller.state.refresh();
  assert.equal(controller.state.liveTransport, 'refreshing');
  const refreshed = structuredClone(initial);
  refreshed.operational.revision = digest('f');
  resolveRefresh({ ok:true, status:200, headers:responseHeaders(etag(nextDigest)), json:async () => refreshed });
  await pendingRefresh;
  const failedRefresh = controller.state.refresh();
  resolveRefresh({ ok:false, status:500, headers:responseHeaders(null) });
  await failedRefresh;
  controller.componentWillUnmount();

  const unavailable = dashboardController({ EventSource:FakeEventSource, fetch:async () => ({ ok:false, status:503, headers:responseHeaders(null) }) });
  unavailable.state.data = initial;
  unavailable.startOperationalStream(initial);
  unavailable.liveController.props.onState = value => { if (value) states.add(value); unavailable.setState({ liveTransport:value }); };
  await nextTurn();
  const denominator = new Set(initial.vocabulary.entries.filter(entry => entry.family === 'live-transport').map(entry => entry.value));
  assert.deepEqual([...states].sort(), [...denominator].sort());
  unavailable.componentWillUnmount();
});

test('file and non-loopback dashboards are inert and teardown aborts late refresh work', async () => {
  const initial = dashboardData();
  for (const location of [{ protocol:'file:', hostname:'' }, { protocol:'http:', hostname:'example.test' }]) {
    let requests = 0;
    let sources = 0;
    class ForbiddenEventSource { constructor() { sources += 1; } }
    const inert = dashboardController({ ...location, EventSource:ForbiddenEventSource, fetch:async () => { requests += 1; return { ok:false }; } });
    inert.state.data = initial;
    inert.startOperationalStream(initial);
    await nextTurn();
    assert.equal(requests, 0);
    assert.equal(sources, 0);
    assert.equal(inert.state.refresh, null);
    assert.equal(inert.state.liveTransport, '');
    inert.componentWillUnmount();
  }

  let resolveHead;
  let headSignal;
  let rapidSources = 0;
  class RapidEventSource { constructor() { rapidSources += 1; } }
  const rapid = dashboardController({
    EventSource:RapidEventSource,
    fetch:(_url, options) => {
      headSignal = options.signal;
      return new Promise(resolve => { resolveHead = resolve; });
    }
  });
  rapid.state.data = initial;
  rapid.startOperationalStream(initial);
  rapid.componentWillUnmount();
  assert.equal(headSignal.aborted, true);
  assert.equal(rapid.state.refresh, null);
  resolveHead({ ok:true, headers:responseHeaders(etag(digest('e'))) });
  await nextTurn();
  assert.equal(rapidSources, 0, 'a late HEAD cannot construct EventSource after unmount');

  let resolveRefresh;
  let refreshSignal;
  class FakeEventSource {
    static instance;
    constructor() { this.handlers = {}; FakeEventSource.instance = this; }
    addEventListener(name, handler) { this.handlers[name] = handler; }
    removeEventListener(name) { delete this.handlers[name]; }
    close() { this.closed = true; }
  }
  const acceptedDigest = digest('c');
  const controller = dashboardController({
    EventSource:FakeEventSource,
    fetch:(_url, options) => {
      if (options.method === 'HEAD') return Promise.resolve({ ok:true, headers:responseHeaders(etag(acceptedDigest)) });
      refreshSignal = options.signal;
      return new Promise(resolve => { resolveRefresh = resolve; });
    }
  });
  controller.state.data = initial;
  controller.startOperationalStream(initial);
  await nextTurn();
  FakeEventSource.instance.handlers.open();
  const hash = controller.testLocation.hash = '#do-not-touch';
  const refresh = controller.state.refresh();
  controller.componentWillUnmount();
  assert.equal(refreshSignal.aborted, true);
  assert.equal(FakeEventSource.instance.closed, true);
  assert.deepEqual(FakeEventSource.instance.handlers, {});
  const stateAfterUnmount = JSON.stringify(controller.state);
  resolveRefresh({ ok:true, status:200, headers:responseHeaders(etag(digest('d'))), json:async () => ({ ...initial, operational:{ ...initial.operational, revision:digest('0') } }) });
  await refresh;
  await nextTurn();
  assert.equal(JSON.stringify(controller.state), stateAfterUnmount);
  assert.equal(controller.testLocation.hash, hash);
});

test('DashboardShell exposes carried live chrome only when refresh is eligible', () => {
  const data = dashboardData();
  data.spaces = [{ id:'carried-order' }];
  data.nodes = [
    { system:'zeta', org:'Zed', spaces:['carried-order'] },
    { system:data.self, org:'Self', spaces:['carried-order'] },
    { system:'alpha', org:'Alpha', spaces:['carried-order'] },
  ];
  const controller = dashboardController({ fetch:async () => ({ ok:false }), EventSource:null });
  controller.state.data = data;
  controller.state.liveTransport = 'newer-available';
  controller.state.refresh = () => {};
  const shell = new controller.testClasses.DashboardShell();
  shell.props = { ctx:controller.renderVals().shellTopCtx };
  let values = shell.renderVals();
  assert.equal(values.hasRefresh, true);
  assert.equal(values.refreshLabel, 'Refresh dashboard');
  assert.equal(values.liveTransportResult.label, 'Newer data available');
  assert.deepEqual(
    Array.from(values.spaceSwitcher.options.find(option => option.label === 'carried-order').faces, face => face.title),
    ['zeta · Zed', data.self + ' · Self', 'alpha · Alpha'],
    'the shell must preserve the producer-owned member order',
  );
  controller.state.locale = 'ru';
  shell.props = { ctx:controller.renderVals().shellTopCtx };
  values = shell.renderVals();
  assert.equal(values.refreshLabel, 'Обновить дашборд');
  controller.state.refresh = null;
  controller.state.liveTransport = '';
  shell.props = { ctx:controller.renderVals().shellTopCtx };
  values = shell.renderVals();
  assert.equal(values.hasRefresh, false);
  assert.equal(values.hasLiveTransport, false);
  assert.equal(values.refreshLabel, '');
  const shellSource = readFileSync(new URL('../design-source/DashboardShell.dc.html', import.meta.url), 'utf8');
  assert.doesNotMatch(shellSource, /no network requests|без сетевых запросов/);
  assert.doesNotMatch(shellSource, /members\.slice\(\)\.sort\(/, 'member order must not be re-derived in the browser');
});

test('P14 Spaces renders carried source, consistency, and freshness facts', () => {
  const controller = dashboardController({ fetch: async () => ({ ok: false }), EventSource: null });
  const data = JSON.parse(readFileSync(new URL('../public/demo-data.json', import.meta.url), 'utf8'));
  data.spaces = [
    { id:'alpha', repoURL:'ssh://example.invalid/alpha.git', synced:true, syncAge:'', stale:false, revision:'revision-alpha', participantCount:2, readable:true, schemaVersion:'space/v1', minBinaryVersion:'0.18.0', workflowVersion:'4', workflowRef:'workflow@4' },
    { id:'beta', repoURL:'ssh://example.invalid/beta.git', synced:false, syncAge:'4h', stale:true, revision:'revision-beta', participantCount:1, readable:true, schemaVersion:'space/v1', minBinaryVersion:'0.17.0', workflowVersion:'3', workflowRef:'workflow@3' },
    { id:'delta', repoURL:'ssh://example.invalid/delta.git', synced:false, syncAge:'', stale:false, revision:'revision-delta', participantCount:0, readable:true, schemaVersion:'space/v1' },
    { id:'gamma', repoURL:'', synced:false, syncAge:'', stale:false, participantCount:0, readable:false },
    { id:'epsilon', repoURL:'', synced:false, syncAge:'', stale:false, participantCount:0, readable:false },
  ];
  data.nodes = [
    { system:'atlas', org:'Northstar', spaces:['alpha','beta'] },
    { system:'checkout', org:'Northstar', spaces:['alpha'] },
  ];
  data.flags = [];
  data.unavailable = [];
  data.operational = {
    revision:'sha256:p14',
    sources:[
      { kind:'space', space:'alpha', revision:'source-alpha', freshness:'current', synced_at:'2026-08-14T09:20:00Z' },
      { kind:'space', space:'beta', revision:'source-beta', freshness:'stale', synced_at:'2026-08-14T08:20:00Z' },
      { kind:'space', space:'delta', revision:'source-delta', freshness:'degraded' },
      { kind:'space', space:'gamma', revision:'source-gamma', freshness:'unavailable' },
    ],
    unavailable:[
      { source_kind:'space', space:'alpha', code:'older-alpha-diagnostic', summary:'An older alpha read was unavailable.' },
      { source_kind:'space', space:'beta', code:'older-beta-diagnostic', summary:'An older beta read was unavailable.' },
      { source_kind:'space', space:'delta', code:'older-delta-diagnostic', summary:'An older delta read was unavailable.' },
      { source_kind:'space', space:'epsilon', code:'space-read-unavailable', summary:'The local mirror could not be read.' },
    ],
    timeline:[
      { space:'alpha', thread:'thread:alpha', consistency:[], consistency_window:{ shown:0, total:0, truncated:false }, work:[] },
      {
        space:'beta', thread:'thread:beta',
        consistency:[
          { code:'future-clock-anomaly', severity:'warning', summary:'The committed report is dated in the future.', subject_ref:'XW-beta' },
          { code:'future-severity-code', severity:'future-severity-token', summary:'A future diagnostic remains technical.', subject_ref:'' },
        ],
        consistency_window:{ shown:2, total:3, truncated:true },
        work:[{ work_id:'work:beta', freshness:'stale' }],
      },
      { space:'delta', thread:'thread:delta', consistency:[], consistency_window:{ shown:0, total:0, truncated:false }, work:[] },
      { space:'gamma', thread:'thread:gamma', consistency:[], consistency_window:{ shown:0, total:0, truncated:false }, work:[] },
      { space:'epsilon', thread:'thread:epsilon', consistency:[], consistency_window:{ shown:0, total:0, truncated:false }, work:[] },
    ],
  };
  controller.state.data = data;
  controller.state.space = 'all';

  class DCLogic {
    setState(update) {
      const patch = typeof update === 'function' ? update(this.state) : update;
      this.state = Object.assign({}, this.state, patch);
    }
  }
  const context = { DCLogic, console, document:{ documentElement:{ lang:'en' } } };
  context.window = context;
  context.globalThis = context;
  vm.runInNewContext(runtimeDesignPage('VocabularyResolver.dc.html').logic, context);
  const spacesLogic = runtimeDesignPage('SpacesView.dc.html').logic;
  vm.runInNewContext(`globalThis.__SpacesView = (() => { ${spacesLogic}\nreturn Component; })();`, context);
  const spaces = new context.__SpacesView();
  const renderSpaces = (ui = {}) => {
    const base = controller.viewContext('SpacesView');
    spaces.props = { ctx:{ ...base, ui:{ ...base.ui, ...ui } } };
    return spaces.renderVals();
  };

  const values = renderSpaces();
  assert.deepEqual(Array.from(values.spaceRows, row => row.id), ['alpha','beta','delta','gamma','epsilon'], 'Spaces must retain carried order');
  const [alpha, beta, delta, gamma, epsilon] = values.spaceRows;
  const technicalValue = (row, label) => row.technical.find(fact => fact.label === label)?.value;
  assert.equal(alpha.sourceResult.label, 'Source is current', 'an empty presentation age must not reclassify exact Source current');
  assert.equal(alpha.sourceSyncedAt, '2026-08-14T09:20:00Z');
  assert.equal(alpha.hasUnavailable, false, 'exact Source current must keep a matching diagnostic Technical-only');
  assert.equal(alpha.sourceDiagnostics[0].technical[0].value, 'older-alpha-diagnostic');
  assert.equal(technicalValue(alpha, 'SpaceHealth.Readable'), 'true');
  assert.equal(technicalValue(alpha, 'SpaceHealth.Stale'), 'false');
  assert.equal(technicalValue(alpha, 'SpaceHealth.Synced'), 'true');
  assert.equal(alpha.healthText, undefined);
  assert.equal(alpha.syncedText, undefined);
  assert.equal(beta.sourceResult.label, 'Source is stale');
  assert.equal(beta.sourceSyncedAt, '2026-08-14T08:20:00Z');
  assert.equal(beta.hasUnavailable, false, 'exact Source stale must keep a matching diagnostic Technical-only');
  assert.equal(delta.sourceResult.label, 'Source degraded');
  assert.equal(delta.hasUnavailable, false, 'exact Source degraded must keep a matching diagnostic Technical-only');
  assert.equal(gamma.sourceResult.label, 'Source unavailable');
  assert.equal(gamma.sourceSyncedAt, 'Not recorded.');
  assert.equal(gamma.unavailable[0].body, 'Unknown in this snapshot.', 'exact unavailable without a public summary must use canonical unknown');
  assert.equal(epsilon.sourceResult.label, 'Source unavailable', 'a matching diagnostic supplies unavailable only when Source is absent');
  assert.equal(epsilon.unavailable[0].body, 'Unavailable in this snapshot: The local mirror could not be read.');
  assert.notEqual(beta.sourceResult.label, gamma.sourceResult.label, 'stale and unavailable must remain visibly distinct carried states');

  assert.equal(beta.workFreshness[0].result.label, 'report expired');
  assert.notEqual(beta.workFreshness[0].result.label, beta.sourceResult.label, 'freshness/stale and source-freshness/stale must use distinct resolver families');
  assert.equal(beta.consistencyGroups[0].countText, '3 anomalies', 'anomaly count must use the carried total rather than the visible array length');
  assert.equal(beta.consistencyGroups[0].windowText, 'Showing 2 of 3.');
  assert.deepEqual(Array.from(beta.consistencyGroups[0].anomalies, anomaly => anomaly.severityResult.label), ['Sources need review','Unknown value']);
  assert.deepEqual(
    Array.from(beta.consistencyGroups[0].anomalies[0].technical, fact => [fact.label, fact.value]),
    [['code','future-clock-anomaly'], ['summary','The committed report is dated in the future.'], ['subject','XW-beta']],
  );
  assert.deepEqual(
    Array.from(beta.consistencyGroups[0].anomalies[1].technical, fact => [fact.label, fact.value]),
    [['code','future-severity-code'], ['summary','A future diagnostic remains technical.'], ['subject','Not recorded.']],
  );
  assert.equal(alpha.consistencyEmpty, true);
  assert.equal(gamma.consistencyUnknown, true, 'an unreadable space must not turn a zero-shaped fixture into a known-empty claim');
  assert.equal(gamma.participantsText, 'Unknown in this snapshot.');

  controller.state.space = 'beta';
  assert.deepEqual(Array.from(renderSpaces().spaceRows, row => row.id), ['beta'], 'user scope selection may filter without reordering');
  controller.state.space = 'all';
  const betaID = controller.cardID('space', data.spaces[1]);
  beta.open();
  assert.equal(JSON.stringify(controller.state.card), JSON.stringify({
    kind:'space', id:betaID, accent:controller.cardAccent('source', ['beta','space']),
  }));
  const cardValues = renderSpaces({ card:controller.state.card });
  assert.equal(cardValues.cardSpace, true);
  assert.equal(cardValues.spaceCard.id, 'beta');
  assert.equal(cardValues.spaceCard.sourceHighlighted, true);
  assert.equal(cardValues.spaceCard.lead, 'Evidence for space beta is Source is stale; it was last synchronized at 2026-08-14T08:20:00Z.');
  assert.equal(cardValues.spaceCard.repository, 'ssh://example.invalid/beta.git');
  assert.equal(cardValues.spaceCard.participantsText, '1 — atlas');
  assert.match(cardValues.spaceCard.compatibilityText, /schema space\/v1; minimum binary 0\.17\.0; workflow 3/);
  assert.equal(cardValues.spaceCard.consistencyCardText, 'Consistency: 3 — Sources need review, Unknown value.');
  assert.deepEqual(Array.from(cardValues.spaceCard.factOrder), ['identity','condition','repository','participants','compatibility','consistency','unavailable','snapshot','technical']);

  controller.state.locale = 'ru';
  const ruCardValues = renderSpaces({ card:controller.state.card });
  assert.equal(ruCardValues.spaceCard.consistencyCardText, 'Согласованность: 3 — Нужно сверить источники, Неизвестное значение.');
  controller.state.locale = 'en';

  controller.openCard('space', betaID, controller.cardAccent('consistency-anomaly', ['beta','thread:beta','future-clock-anomaly','XW-beta']));
  assert.equal(renderSpaces({ card:controller.state.card }).spaceCard.consistencyGroups[0].anomalies[0].highlighted, true, 'opaque anomaly accents must select the carried anomaly without reordering it');

  const spacesSource = readFileSync(new URL('../design-source/SpacesView.dc.html', import.meta.url), 'utf8');
  const actualFactOrder = Array.from(runtimeDesignPage('SpacesView.dc.html').template.matchAll(/data-space-card-fact="([^"]+)"/g), match => match[1]);
  assert.deepEqual(actualFactOrder, ['identity','condition','repository','participants','compatibility','consistency','unavailable','snapshot','technical'], 'actual space-card DOM must follow P5 order');
  assert.match(spacesSource, /<details aria-label="\{\{ anomaly\.technicalAria \}\}"/);
  for (const match of spacesSource.matchAll(/<dc-import name="FreshnessDot"([^>]*)>/g)) {
    assert.match(match[1], /result="\{\{/);
    assert.match(match[1], /age="\{\{/);
    assert.match(match[1], /compact="\{\{/);
    assert.doesNotMatch(match[1], /\s(?:stale|hint)="/, 'FreshnessDot call sites may pass only resolver result plus age/compact presentation props');
  }
  assert.doesNotMatch(spacesSource, /pillText|pillTone|freshnessText|\.sort\(|Date\.now|new Date|\.consistency\.length|revision\.slice/);
  assert.doesNotMatch(spacesSource, /healthText|syncedText|words\.(?:readable|stale|synced|yes|no)\b/, 'SpaceHealth booleans must remain exact Technical facts rather than body-local status wording');
  assert.match(spacesSource, /resolve\("freshness", work\.freshness\)/);
  assert.match(spacesSource, /resolve\("source-freshness", sourceUnavailable \? "unavailable" : carriedSourceFreshness\)/);
  assert.match(spacesSource, /resolve\("consistency-severity", anomaly\.severity\)/);
  const shellSource = readFileSync(new URL('../design-source/DashboardShell.dc.html', import.meta.url), 'utf8');
  assert.doesNotMatch(shellSource, /statusDotStyle|healthDotStyle/, 'the frozen SpaceSwitcher caller must pass resolver results rather than legacy status treatments');
});

test('operational rows expose honest freshness metadata and navigate from process and work subjects', () => {
  const controller = dashboardController({ fetch: async () => ({ ok: false }), EventSource: null });
  const data = JSON.parse(readFileSync(new URL('../public/demo-data.json', import.meta.url), 'utf8'));
  data.operational = {
    generated_at: '2026-08-03T12:00:00Z',
    revision: 'sha256:operational', unavailable: [{ source_kind:'space', space:'checkout-core', code:'partial-read', summary:'One auxiliary read was unavailable' }],
    sources: [{ kind: 'space', space: 'checkout-core', freshness: 'stale', synced_at:'2026-08-03T11:58:00Z' }],
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
  assert.equal(values.operationalSnapshot, 'snapshot 2026-08-03T12:00:00Z', 'Overview must not derive a second source verdict in snapshot chrome');
  const row = values.operationalRows[0];
  assert.equal(row.waitingOn, 'checkout');
  assert.equal(row.sourceResult.label, 'Source is stale');
  assert.equal(row.sourceUnavailableText, '', 'matching diagnostic does not override an authoritative stale Source');
  assert.equal(row.sourceSynced, 'synchronized 2026-08-03T11:58:00Z');
  assert.deepEqual(Array.from(row.work, work => work.freshnessResult.label), ['observed locally', 'committed in Git']);
  assert.deepEqual(Array.from(row.work, work => work.modeResult.label), ['Testing the result', 'Implementing now']);
  assert.match(row.work[0].evidenceText, /reported 2026-08-03T11:45:00Z/);
  assert.match(row.work[0].evidenceText, /observed 2026-08-03T11:59:00Z/);
  assert.match(row.work[0].evidenceText, /valid until 2026-08-03T12:15:00Z/);

  const processID = controller.cardID('operational-process', data.operational.timeline[0]);
  row.openCard();
  assert.equal(controller.state.card.kind, 'operational-process');
  assert.equal(controller.state.card.id, processID);
  assert.equal(controller.state.card.accent, '');
  row.work[1].openCard();
  assert.equal(controller.state.card.kind, 'operational-process');
  assert.equal(controller.state.card.id, processID);
  assert.equal(controller.state.card.accent, controller.cardAccent('work-row', data.operational.timeline[0].work[1].work_id));
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
  assert.equal(row.work.length, 1);
  assert.equal(row.work[0].modeResult.label, 'Work paused');
  assert.equal(row.work[0].freshnessResult.label, 'publication needs recovery');
  assert.notEqual(row.work[0].freshnessResult.label, 'observed locally');
  assert.match(row.work[0].evidenceText, /current: no/);
  assert.match(row.work[0].evidenceText, /reported Not recorded\./);
  assert.match(row.work[0].evidenceText, /observed Not recorded\./);
  assert.match(row.work[0].evidenceText, /valid until Not recorded\./);
  assert.equal(row.sharedPendingEmptyText, 'Not recorded.');
  const overviewSource = readFileSync(new URL('../design-source/Overview.dc.html', import.meta.url), 'utf8');
  assert.doesNotMatch(overviewSource, /hasCurrentWork|currentWork|historicalWork|currentUnknownText/, 'Overview must not reclassify carried recovery work into current/history buckets');
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
