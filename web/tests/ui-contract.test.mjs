import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';
import { injectCardManifest, runtimeDesignPage } from '../src/lib/design-source.ts';

const repoFile = path => readFileSync(new URL(`../../${path}`, import.meta.url), 'utf8');
const tokens = repoFile('ui/tokens.css');
const dashboardSource = injectCardManifest(repoFile('web/design-source/14-local-dashboard-v4.dc.html'));
const publicHomeSource = repoFile('web/design-source/13-public-home-v4.dc.html');
const siteContent = repoFile('web/content/site.json');

const TOKEN_GROUPS = {
  titles: [
    '--font-card-title-md', '--line-card-title-md',
    '--font-card-title-sm', '--line-card-title-sm',
    '--font-section-title', '--line-section-title',
    '--font-section-subtitle', '--line-section-subtitle',
    '--font-subheading', '--line-subheading',
    '--font-meta', '--line-meta'
  ],
  radii: ['--radius-badge', '--radius-nested', '--radius-card', '--radius-panel'],
  padding: [
    '--padding-panel-block', '--padding-panel-inline',
    '--padding-card-block', '--padding-card-inline',
    '--padding-list-card-block', '--padding-list-card-inline',
    '--padding-nested-block', '--padding-nested-inline',
    '--padding-modal-backdrop'
  ],
  badges: [
    '--font-badge-default', '--font-badge-small', '--line-badge',
    '--padding-badge-solid-block', '--padding-badge-solid-inline',
    '--padding-badge-outline-block', '--padding-badge-outline-inline'
  ],
  rhythm: ['--space-grid', '--space-stack', '--space-inline'],
  selection: ['--surface-inactive-card', '--border-inactive-card', '--shadow-inactive-card']
};

const TOKEN_NAMES = Object.values(TOKEN_GROUPS).flat();

function occurrences(source, token) {
  return source.match(new RegExp(`${token.replaceAll('-', '\\-')}\\s*:`, 'g'))?.length || 0;
}

function component(name) {
  return repoFile(`web/design-source/${name}.dc.html`);
}

function componentHarness(name, props) {
  class DCLogic {
    setState(update) {
      const patch = typeof update === 'function' ? update(this.state) : update;
      this.state = Object.assign({}, this.state, patch);
    }
  }
  const context = {
    DCLogic,
    console,
    document: { documentElement: { lang: 'en' }, body: { style:{} }, activeElement:null, addEventListener() {}, removeEventListener() {} },
    addEventListener() {}, removeEventListener() {}, innerWidth: 1280, innerHeight: 800,
    requestAnimationFrame(callback) { callback(); },
    A2A_VOCABULARY_RESOLVER: {
      lookup() { return { label:'unknown value', explanation:'unknown', toneClass:'tone-unknown', cue:'?', cueAttribute:'?' }; },
      presentation() {
        return {
          pillBackground:'var(--resolver-pill-bg)', pillForeground:'var(--resolver-pill-fg)', pillRing:'none',
          dotForeground:'var(--resolver-dot-fg)', dotBackground:'var(--resolver-dot-bg)',
          markerColor:'var(--resolver-marker)', signalTone:'negative'
        };
      }
    },
  };
  context.window = context;
  context.globalThis = context;
  const logic = runtimeDesignPage(`${name}.dc.html`).logic;
  vm.runInNewContext(`globalThis.__Part = (() => { ${logic}\nreturn Component; })();`, context);
  const part = new context.__Part();
  part.props = props;
  return { part, values:part.renderVals(), context };
}

function componentValues(name, props) {
  return componentHarness(name, props).values;
}

function dashboardController({ hash = '' } = {}) {
  class DCLogic {
    setState(update) {
      const patch = typeof update === 'function' ? update(this.state) : update;
      this.state = Object.assign({}, this.state, patch);
    }
  }
  const context = {
    DCLogic,
    EventSource: null,
    fetch: async () => ({ ok: false }),
    console,
    setTimeout,
    clearTimeout,
    URLSearchParams,
    requestAnimationFrame(callback) { callback(); },
    document: {
      documentElement: { lang: 'en', dataset: {}, setAttribute() {} },
      body: { dataset: {} },
      addEventListener() {},
      removeEventListener() {},
      querySelector() { return null; },
      getElementById() { return null; },
    },
    location: { protocol: 'http:', hostname: '127.0.0.1', pathname:'/dashboard.html', search: '', hash },
    localStorage: { getItem() { return null; }, setItem() {} },
    navigator: { clipboard: { writeText: async () => {} } },
  };
  context.addEventListener = () => {};
  context.removeEventListener = () => {};
  context.scrollTo = () => {};
  context.window = context;
  context.globalThis = context;
  const resolverImport = dashboardSource.match(/<dc-import name="(VocabularyResolver)"[^>]*><\/dc-import>/);
  assert.ok(resolverImport, 'root must literally import the vocabulary resolver');
  const resolverLogic = runtimeDesignPage(`${resolverImport[1]}.dc.html`).logic;
  vm.runInNewContext(`(() => { ${resolverLogic}\nreturn Component; })();`, context);
  assert.equal(typeof context.A2A_VOCABULARY_RESOLVER?.lookup, 'function', 'root resolver import must expose one lookup');
  const projectionSource = repoFile('web/src/lib/design-source.ts');
  assert.doesNotMatch(projectionSource, /viewComponents|projectionNames/, 'public projection must derive components from root literal imports');
  const rootViews = [...dashboardSource.matchAll(/<dc-import name="([A-Z][A-Za-z0-9]*)" ctx="\{\{ ([A-Za-z][A-Za-z0-9]*) \}\}"[^>]*><\/dc-import>/g)]
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
    const raw = name === 'Dashboard' ? injectCardManifest(component('14-local-dashboard-v4')) : '';
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
  controller.statusOf = bindViewHelper('Overview', 'statusOf');
  controller.detailFor = bindViewHelper('ExchangeView', 'detailFor');
  controller.threadsValues = (ui = {}) => {
    const view = views.ThreadsView;
    const base = controller.viewContext('ThreadsView');
    view.props = { ctx: { ...base, ui: { ...base.ui, ...ui } } };
    return view.renderVals();
  };
  controller.resolveVocabulary = (data, family, value, locale, policy) => context.A2A_VOCABULARY_RESOLVER.lookup(data, family, value, locale, policy);
  controller.vocabularyPolicy = context.A2A_VOCABULARY_RESOLVER.defaultPolicy;

  const live = new classes.DashboardLive();
  controller.startOperationalStream = data => {
    live.props = {
      data,
      onHydrate: candidate => controller.hydrate(candidate),
      onState: liveTransport => controller.setState({ liveTransport }),
      onReady: refresh => controller.setState({ refresh })
    };
    live.startOperationalStream(data);
  };
  const unmountRoot = controller.componentWillUnmount.bind(controller);
  controller.componentWillUnmount = () => {
    live.componentWillUnmount();
    unmountRoot();
  };
  for (const method of ['renderVals', 'viewContext', 'hydrate', 'startOperationalStream', 'componentWillUnmount', 'resolveVocabulary', 'cardID', 'cardAccent', 'resolveCard', 'openCard', 'closeCard', 'openAggregate', 'sourceURL']) {
    assert.equal(typeof controller[method], 'function', `dashboard façade is missing ${method}`);
  }
  controller.testLocation = context.location;
  return controller;
}

function exchangeSubjectFixture() {
  const space = 'space-a';
  const thread = 'thread:shared';
  const artifact = (id, title, type = 'announcement') => ({
    sourceClass: 'space', space, id, type, title, from: 'atlas', to: ['checkout'], state: 'published', thread,
    envelope: { created: '2026-08-04T09:00:00Z' }, body: '', bodyHTML: '', events: [], flags: [], refs: [],
  });
  const item = (id, title, type = 'announcement') => ({
    space, id, type, title, from: 'atlas', to: ['checkout'], state: 'published', thread, priority: 'p2',
  });
  const report = (artifactID, subjectRef, summary, commitSequence, reportedAt) => ({
    space, thread, artifact_id: artifactID, work_id: `work:${artifactID}`, subject_ref: subjectRef,
    mode: 'implementing', summary, actor: { kind: 'agent', name: 'codex', system: 'atlas' }, waiting_on: [],
    reported_at: reportedAt, commit_sequence: commitSequence,
  });
  const artifacts = [
    artifact('XW-target', 'Target artifact', 'work_request'),
    artifact('XA-report-artifact', 'Artifact checkpoint'),
    artifact('XA-report-thread', 'Thread checkpoint'),
    artifact('XA-report-contract', 'Contract checkpoint'),
    artifact('XA-b', 'Tie B'),
    artifact('XA-z', 'Tie Z'),
    artifact('XA-old', 'Older commit'),
  ];
  const inbox = artifacts.map(a => item(a.id, a.title, a.type));
  return {
    self: 'atlas', generatedAt: '2026-08-04T12:00:00Z',
    vocabulary: {
      entries: [{
        family:'transition', value:'note', labelEN:'note added', labelRU:'заметка добавлена',
        explanationEN:'An informational note was added to the thread; by itself it does not determine protocol state.',
        explanationRU:'К цепочке добавлена информационная заметка; сама по себе она не определяет состояние протокола.',
        tone:'progressing', cue:'›',
      }],
      unknown:{ labelEN:'Unknown in this snapshot.', labelRU:'В этом снимке неизвестно.', explanationEN:'Unknown vocabulary value.', explanationRU:'Неизвестное значение словаря.', tone:'unknown', cue:'?' },
    },
    spaces: [{ id: space, readable: true }],
    nodes: [{ system: 'atlas', owners: ['atlas'] }, { system: 'checkout', owners: ['checkout'] }],
    threads: [{ id: thread, space, opener: { id: 'XW-target', title: 'Shared thread' }, participants: ['atlas', 'checkout'], memberCount: 7 }],
    threadViews: [{ thread, space, order: 'committed', opener: { id: 'XW-target', title: 'Shared thread', from: 'atlas' }, participants: ['atlas', 'checkout'], artifacts: [], transcript: [], open_items: [], flags: [], unresolved: [] }],
    inbox, outbox: [], archive: [], contracts: [{ id: 'XC-orders', space }], flags: [], artifactDetails: artifacts,
    workReports: [
      report('XA-report-artifact', 'XW-target', 'artifact subject', 7, '2026-08-04T11:00:00Z'),
      report('XA-report-thread', thread, 'thread subject', 8, '2026-08-04T11:01:00Z'),
      report('XA-report-contract', 'XC-orders@2.4.0', 'contract subject', 9, '2026-08-04T11:02:00Z'),
      report('XA-z', 'XW-target', 'tie z', 11, '2026-08-04T08:00:00Z'),
      report('XA-b', 'XW-target', 'tie b', 11, '2026-08-04T07:00:00Z'),
      report('XA-old', 'XW-target', 'older commit but newer authored time', 10, '2026-08-04T12:00:00Z'),
    ],
  };
}

test('the release UI token contract has one canonical declaration and an exact renderer mirror', () => {
  assert.equal(repoFile('internal/html/tokens.css'), tokens);
  for (const token of TOKEN_NAMES) {
    assert.equal(occurrences(tokens, token), 1, `${token} must have exactly one canonical declaration`);
  }
});

test('badge components consume the same shared geometry and type contract', () => {
  const badgeFiles = [component('TypeBadge'), component('StatusPill')];
  for (const source of badgeFiles) {
    for (const token of [...TOKEN_GROUPS.badges, '--radius-badge']) {
      assert.match(source, new RegExp(`var\\(${token.replaceAll('-', '\\-')}\\)`), `${token} missing from badge component`);
    }
  }
});

test('shared status primitives give resolver results precedence over legacy presentation props', () => {
  const result = { label:'Carried label', explanation:'Carried explanation', toneClass:'tone-progressing', cue:'›', cueAttribute:'›' };
  const pill = componentValues('StatusPill', { result, text:'legacy label', tone:'blocking', why:'legacy explanation' });
  assert.deepEqual(
    { text:pill.text, why:pill.why, toneClass:pill.toneClass, cue:pill.cue, cueAttribute:pill.cueAttribute },
    { text:'Carried label', why:'Carried explanation', toneClass:'tone-progressing', cue:'›', cueAttribute:'›' }
  );
  assert.match(pill.pillStyle, /--resolver-pill-bg/);
  assert.match(pill.pillStyle, /--resolver-pill-fg/);
  const dot = componentValues('FreshnessDot', { result, stale:true, hint:'legacy explanation', age:'2m', compact:false });
  assert.equal(dot.toneClass, 'tone-progressing');
  assert.equal(dot.cueAttribute, '›');
  assert.match(dot.hint, /^Carried explanation/);
  assert.doesNotMatch(dot.hint, /legacy/);
  assert.match(dot.dotStyle, /--resolver-dot-fg/);
  assert.match(dot.dotStyle, /--resolver-dot-bg/);
  const switcher = componentValues('SpaceSwitcher', { det:{ options:[{ label:'space-a', selected:true, result, statusDotStyle:'background:red;' }], healthResult:result, healthDotStyle:'background:red;' } });
  assert.equal(switcher.statusToneClass, 'tone-progressing');
  assert.equal(switcher.statusCueAttribute, '›');
  assert.doesNotMatch(switcher.statusDotStyle, /red/);
  assert.match(switcher.statusDotStyle, /--resolver-marker/);
  assert.equal(switcher.healthToneClass, 'tone-progressing');
  assert.doesNotMatch(switcher.healthDotStyle, /red/);
  const signal = componentValues('EventSignal', { result, tone:'positive', label:'legacy signal' });
  assert.equal(signal.negative, true);
  assert.equal(signal.positive, false);
});

test('Modal dispatches exactly one manifest-selected card host with a narrow card context', () => {
  const manifest = JSON.parse(repoFile('web/design-source/cards.manifest.json'));
  const card = { kind:'thread', id:'opaque', accent:'' };
  const currentData = { marker:'current', flags:[], spaces:[], nodes:[], inbox:[], outbox:[], archive:[], contracts:[], contractEdges:[], tooling:{} };
  const retainedCardData = { marker:'retained', flags:[], spaces:[], nodes:[], inbox:[], outbox:[], archive:[], contracts:[], contractEdges:[], tooling:{} };
  const modal = componentValues('Modal', {
    ctx: {
      data:currentData,
      locale:'en', ui:{ card, cardManifest:manifest }, actions:{ closeCard() {} }
    }
  });
  assert.equal([modal.cardArtifactDetail, modal.cardThreadsView, modal.cardContractsView, modal.cardExchangeView, modal.cardSpacesView, modal.cardOverview].filter(Boolean).length, 1);
  assert.equal(modal.cardThreadsView, true);
  assert.deepEqual(Object.keys(modal.cardCtx).sort(), ['actions','data','locale','ui']);
  assert.deepEqual(Object.keys(modal.cardCtx.ui), ['card']);
  assert.equal(modal.cardCtx.ui.card, card);
  assert.equal(modal.cardCtx.data, currentData);
  assert.equal(modal.cardGone, false);

  const goneEN = componentValues('Modal', {
    ctx:{ data:currentData, locale:'en', ui:{ card, cardManifest:manifest, goneCard:true, retainedCardData }, actions:{ closeCard() {} } }
  });
  assert.equal(goneEN.cardCtx.data, retainedCardData);
  assert.equal(goneEN.cardCtx.ui.card, card);
  assert.equal(goneEN.cardGone, true);
  assert.equal(goneEN.cardGoneText, 'This card is no longer present in the current data. It remains open so you can finish reading.');
  const goneRU = componentValues('Modal', {
    ctx:{ data:currentData, locale:'ru', ui:{ card, cardManifest:manifest, goneCard:true, retainedCardData }, actions:{ closeCard() {} } }
  });
  assert.equal(goneRU.cardGoneText, 'Этой карточки больше нет в текущих данных. Она остаётся открытой, чтобы вы могли закончить чтение.');

  const template = runtimeDesignPage('Modal.dc.html').template;
  const banner = template.indexOf('data-card-gone');
  const cardHost = template.indexOf('data-card-modal');
  const family = template.indexOf('name="ThreadsView"');
  assert.ok(cardHost >= 0 && banner > cardHost && family > banner, 'gone status belongs inside the canonical card host before card content');
  assert.match(template.slice(cardHost, banner), /overflow-anchor:none/, 'late localized card growth must not move the preserved document scroll position');
  const bannerTag = template.slice(template.lastIndexOf('<', banner), template.indexOf('>', banner) + 1);
  assert.match(bannerTag, /role="status"/);
  assert.doesNotMatch(bannerTag, /tabindex|autofocus|onClick|href=/);
  assert.doesNotMatch(component('Modal'), /mapDocument|threadNote|spaceModal|blockedWhy|cmpVer|\.sort\(/, 'retired overlay engines and their local derivation must leave the one modal host');
});

test('Modal traps card focus, owns body scroll, and restores the opener', () => {
  const manifest = JSON.parse(repoFile('web/design-source/cards.manifest.json'));
  let closes = 0;
  const props = {
    ctx: {
      data:{ flags:[], spaces:[], nodes:[], inbox:[], outbox:[], archive:[], contracts:[], contractEdges:[], tooling:{} },
      locale:'en', ui:{ card:{ kind:'thread', id:'opaque', accent:'' }, cardManifest:manifest }, actions:{ closeCard() { closes += 1; } }
    }
  };
  const { part, values, context } = componentHarness('Modal', props);
  context.document.body.style.overflow = 'auto';
  const opener = { focused:false, focus() { this.focused = true; context.document.activeElement = this; } };
  const first = { focus() { context.document.activeElement = this; } };
  const last = { focus() { context.document.activeElement = this; } };
  const host = {
    focus() { context.document.activeElement = this; },
    contains(node) { return node === this || node === first || node === last; },
    querySelectorAll() { return [first, last]; }
  };
  context.document.activeElement = opener;
  values.cardHostRef(host);
  part.componentDidMount();
  assert.equal(context.document.body.style.overflow, 'hidden');
  assert.equal(context.document.activeElement, host);
  let prevented = false;
  part.trapCardTab({ shiftKey:true, preventDefault() { prevented = true; } });
  assert.equal(prevented, true);
  assert.equal(context.document.activeElement, last, 'Shift+Tab from the dialog host wraps to the final control');
  context.document.activeElement = host;
  prevented = false;
  part.trapCardTab({ shiftKey:false, preventDefault() { prevented = true; } });
  assert.equal(prevented, true);
  assert.equal(context.document.activeElement, first, 'Tab from the dialog host enters the first control');
  props.ctx.ui.goneCard = true;
  props.ctx.ui.retainedCardData = { marker:'retained' };
  part.componentDidUpdate();
  assert.equal(context.document.activeElement, first, 'gone-card adoption must not steal focus inside the already-open dialog');
  part.onEscape({ key:'Escape' });
  assert.equal(closes, 1);
  values.dismissCard({ target:host, currentTarget:host });
  assert.equal(closes, 2, 'backdrop dismissal uses the same close action');
  props.ctx.ui.card = null;
  part.componentDidUpdate();
  assert.equal(context.document.body.style.overflow, 'auto');
  assert.equal(opener.focused, true);
  part.componentWillUnmount();
});

test('DashboardShell clears the aggregate selection through both space controls', () => {
  const source = component('DashboardShell');
  assert.doesNotMatch(source, /scopedInbox|scopedOutbox|needsYou|restCount|offCurrent|scopedEdges/);
  assert.doesNotMatch(source, /\.filter\([^\n]*(blocking|gatePending|overdue|drift)/, 'shared chrome must not reconstruct domain membership or status counts');
  assert.doesNotMatch(source, /spaceModal/, 'space health must use the canonical space card, not a second dialog state');
  assert.doesNotMatch(component('14-local-dashboard-v4'), /mapDocument|threadNote|spaceModal/, 'root UI state must contain only surviving overlay identities');
  assert.doesNotMatch(component('ThreadsView'), /threadNote/, 'thread rows must not target the retired ArtifactDetail overlay');
  const patches = [];
  const cards = [];
  const data = {
    self:'atlas', generatedAt:'2026-08-14T00:00:00Z', meta:{}, tooling:{}, vocabulary:{ entries:[], unknown:{} },
    spaces:[{ id:'space-a' }], nodes:[], inbox:[], outbox:[], archive:[], contracts:[], contractEdges:[], flags:[], operational:{ sources:[{ kind:'space', space:'space-a', freshness:'current' }] }
  };
  const actions = {
    patch(value) { patches.push(value); },
    cardID(kind, source) { cards.push({ op:'id', kind, source }); return 'opaque-space'; },
    cardAccent(family, identity) { cards.push({ op:'accent', family, identity }); return 'opaque-source'; },
    openCard(kind, id, accent) { cards.push({ op:'open', kind, id, accent }); }
  };
  const shell = componentValues('DashboardShell', {
    ctx:{ data, locale:'en', ui:{ space:'all', view:'overview', dashboardSet:'needYou' }, actions }
  });
  const allSwitcher = componentValues('SpaceSwitcher', { det:shell.spaceSwitcher });
  assert.equal(allSwitcher.hasHealth, false, 'all spaces has no carried singular health result to open');
  shell.spaceSwitcher.options.find(option => option.label === 'space-a').go();
  shell.spaceSwitcher.onChange('space-a');
  assert.equal(patches.length, 2);
  assert.equal(patches[0].dashboardSet, '');
  assert.equal(patches[1].dashboardSet, '');

  const scoped = componentValues('DashboardShell', {
    ctx:{ data, locale:'en', ui:{ space:'space-a', view:'overview', dashboardSet:'' }, actions }
  });
  assert.equal(componentValues('SpaceSwitcher', { det:scoped.spaceSwitcher }).hasHealth, true);
  scoped.spaceSwitcher.openHealth();
  assert.deepEqual(JSON.parse(JSON.stringify(cards)), [
    { op:'id', kind:'space', source:data.spaces[0] },
    { op:'accent', family:'source', identity:['space-a','space'] },
    { op:'open', kind:'space', id:'opaque-space', accent:'opaque-source' }
  ]);
});

test('shared card, panel and modal components consume semantic hierarchy tokens', () => {
  const timeline = component('TimelineArtifactCard');
  assert.match(timeline, /var\(--font-card-title-md\)/);
  assert.match(timeline, /var\(--line-card-title-md\)/);
  assert.match(timeline, /var\(--radius-nested\)/);
  assert.match(timeline, /var\(--padding-nested-block\)/);

  // Operator-approved P13 change: map details now use the canonical P12 card,
  // so LinkDetail and NetworkMap no longer own a second panel/modal hierarchy.
});

test('P13 map routes a contract relation to the canonical version card without modal markup', () => {
  const calls = [];
  const edge = {
    space:'space-a', from:'fulfillment', to:'atlas', contract:'XC-orders',
    pinnedVersion:'1.5.0', drift:'retired', description:'Carried relation.'
  };
  const mapView = componentValues('MapView', {
    ctx:{
      data:{}, locale:'en', ui:{ space:'all' },
      actions:{
        cardID(kind, source) { calls.push({ op:'cardID', kind, source }); return 'opaque-cver'; },
        cardAccent(family, identity) { calls.push({ op:'cardAccent', family, identity }); return 'opaque-relation'; },
        openCard(kind, id, accent) { calls.push({ op:'openCard', kind, id, accent }); }
      }
    }
  });
  const data = {
    self:'atlas',
    spaces:[{ id:'space-a', readable:true }],
    nodes:[
      { system:'atlas', self:true, org:'example', status:'active', spaces:['space-a'] },
      { system:'fulfillment', self:false, org:'example', status:'active', spaces:['space-a'] },
      { system:'checkout', self:false, org:'example', status:'active', spaces:['space-a'] },
      { system:'catalog', self:false, org:'example', status:'active', spaces:['space-a'] }
    ],
    contracts:[], contractEdges:[edge], exchangeEdges:[
      { space:'space-a', from:'atlas', to:'checkout', count:1, maxPriority:'p2' },
      { space:'space-a', from:'atlas', to:'catalog', count:2, maxPriority:'p3', owedCount:0 }
    ], inbox:[], outbox:[], unavailable:[],
    windows:{ exchangeEdges:{ shown:2, total:2, truncated:false }, flags:{ shown:0, total:0, truncated:false } }
  };
  const network = componentHarness('NetworkMap', {
    data, space:'all', locale:'en', showSpaceTabs:false,
    onOpenContract:mapView.openContract
  });
  network.part.state.boxW = 1080;
  const networkValues = network.part.renderVals();
  const relation = networkValues.edges.find(row => row.label === 'fulfillment → atlas');
  assert.ok(relation, 'the carried contract relation must remain a rendered map strand');
  relation.go();

  const missingOwedCount = networkValues.tableRows.find(row => row.from === 'atlas' && row.to === 'checkout');
  const exactZeroOwedCount = networkValues.tableRows.find(row => row.from === 'atlas' && row.to === 'catalog');
  assert.ok(missingOwedCount && exactZeroOwedCount, 'both carried exchange edges must remain visible and ordered');
  assert.equal(missingOwedCount.why, '1 live document from atlas to checkout.', 'missing owedCount must omit the count instead of inventing a raw 0');
  assert.equal(exactZeroOwedCount.why, '2 live documents from atlas to catalog. Moves owed: 0.', 'an exact carried owedCount zero must remain visible');

  assert.deepEqual(JSON.parse(JSON.stringify(calls)), [
    { op:'cardID', kind:'cver', source:{ space:'space-a', id:'XC-orders', version:'1.5.0' } },
    { op:'cardAccent', family:'dependency-line', identity:['space-a','fulfillment','atlas','XC-orders'] },
    { op:'openCard', kind:'cver', id:'opaque-cver', accent:'opaque-relation' }
  ]);

  const actual = dashboardController();
  const contract = {
    space:'space-a', id:'XC-orders', provider:'atlas', version:'1.5.0', state:'published', consumers:['fulfillment'],
    versions:[{ version:'1.5.0', state:'retired', detail:{ status:'available', totalDocumentCount:0, omittedDocumentCount:0, documents:[], history:[], consumerPins:[{ system:'fulfillment', space:'space-a', pinnedVersion:'1.5.0', drift:'retired', pinnedState:'retired' }] } }]
  };
  const coherentData = {
    ...data, contracts:[contract], contractEdges:[edge], flags:[], archive:[], outbox:[], workReports:[], threadViews:[], threads:[], artifactDetails:[],
    operational:{ revision:'sha256:fixture', sources:[], timeline:[], unavailable:[] },
    vocabulary:{ entries:[], unknown:{ labelRU:'Неизвестное значение', labelEN:'Unknown value', explanationRU:'Неизвестно', explanationEN:'Unknown', tone:'unknown', cue:'?' } }
  };
  actual.state.data = coherentData;
  const exactID = actual.cardID('cver', { space:edge.space, id:edge.contract, version:edge.pinnedVersion });
  const relationAccent = actual.cardAccent('dependency-line', [edge.space, edge.from, edge.to, edge.contract]);
  actual.openCard('cver', exactID, relationAccent);
  assert.equal(actual.state.card.accent, relationAccent, 'cver manifest must preserve the map relation accent');
  assert.match(actual.testLocation.hash, /^#card=/);
  const reloaded = dashboardController({ hash:actual.testLocation.hash });
  reloaded.restoreCardFromHash();
  assert.deepEqual(JSON.parse(JSON.stringify(reloaded.state.card)), JSON.parse(JSON.stringify(actual.state.card)), 'relation accent survives hash reload');

  const contractValues = componentValues('ContractsView', {
    ctx:{ data:coherentData, locale:'en', ui:{ card:actual.state.card }, actions:{
      cardID:(kind, source) => actual.cardID(kind, source),
      cardAccent:(family, identity) => actual.cardAccent(family, identity),
      openCard:() => {}, copy:() => {}, sourceURL:() => ''
    } }
  });
  assert.equal(contractValues.contractVersionCard.consumerPins[0].highlighted, true, 'the exact-version consumer row is the relation accent target');

  const map = component('NetworkMap');
  assert.doesNotMatch(map, /role="dialog"|aria-modal|padding-modal-backdrop|name="LinkDetail"|popDismiss|popStop/);
  assert.doesNotMatch(map, /blockedWhy|DRIFT_TONE|STATE_TONE|\bRISK\b|\.sort\(/);
  assert.doesNotMatch(map, /owedCount\s*(?:\|\||\?\?)\s*0/, 'missing owedCount must never default to zero');
  assert.throws(() => component('LinkDetail'), /ENOENT/);
  assert.throws(() => component('LinkDetailRow'), /ENOENT/);
});

test('inactive master-list records use the shared contrast-safe container cue', () => {
  const row = component('ArtifactRow');
  assert.match(row, /var\(--surface-inactive-card\)/);
  assert.match(row, /var\(--shadow-inactive-card\)/);
  assert.doesNotMatch(row, /opacity-inactive-card/);
  assert.doesNotMatch(row, /opacity\s*:/);
});

test('P10 Exchange renders item and work-report facts through one content-only ArtifactDetail', () => {
  const item = {
    space:'space-a', id:'XW-second', type:'work_request', title:'Second carried item', from:'atlas', to:['checkout'],
    state:'submitted', outcome:'open', terminal:false, archived:false, priority:'p1', blocking:true, overdue:false,
    waitingOn:['checkout','billing'], expectedTransition:'accept', yourMove:false, humanGate:'G3', gatePending:true,
    reasonSentence:{ en:'Checkout and billing must accept this request.', ru:'Checkout и billing должны принять этот запрос.' },
    why:'pendency/table: work_request/submitted', ruleIdentity:{ kind:'work_request', state:'submitted' },
    description:'Untrusted <b>summary</b>', operationalItems:[{ name:'endpoint', state:'ready' }, { name:'runbook', state:'absent' }],
    neededBy:'2026-08-20T00:00:00Z', pendingMerge:false, syncStale:true, age:'3m', createdAt:'2026-08-14T10:00:00Z',
  };
  const first = { ...item, id:'XW-first', title:'First underlying collection item', createdAt:'2026-08-14T12:00:00Z' };
  const reasonless = { ...item, id:'XW-reasonless', title:'Reason is absent in EN', reasonSentence:{ en:'', ru:'Не переносить в английский.' } };
  const reports = [
    {
      space:'space-a', thread:'thread:shared', artifact_id:'XA-carried-first', work_id:'work:1', subject_ref:'XW-second',
      mode:'waiting', summary:'First carried checkpoint', actor:{ kind:'agent', name:'codex', system:'atlas', model:'gpt-5' },
      waiting_on:[{ kind:'system', id:'checkout', summary:'Receipt proof' }, { kind:'human', id:'owner', summary:'Approve rollout' }],
      reported_at:'2026-08-14T09:00:00Z', valid_until:'2026-08-14T11:00:00Z', commit_sequence:1,
    },
    {
      space:'space-a', thread:'thread:shared', artifact_id:'XA-carried-second', work_id:'work:2', subject_ref:'XW-second',
      mode:'implementing', summary:'Second carried checkpoint', actor:{ kind:'agent', name:'codex', system:'atlas' }, waiting_on:[],
      reported_at:'2026-08-14T12:00:00Z', commit_sequence:99,
    },
  ];
  const data = {
    self:'atlas', generatedAt:'2026-08-14T12:30:00Z', vocabulary:{ entries:[], unknown:{} }, operational:{
      sources:[{ kind:'space', space:'space-a', freshness:'current' }],
      timeline:[{ space:'space-a', thread:'thread:shared', work:[{
        work_id:'work:conflict', current:true, freshness:'finished',
        committed_checkpoint:{ artifact_id:'XA-carried-first' },
        shared_pending:{ remaining_action:'wrong alternate identity', stage:'wrong', state:'wrong' },
      }, {
        work_id:'work:1', current:false, freshness:'committed-current',
        committed_checkpoint:{ artifact_id:'XA-carried-first' },
        shared_pending:{ remaining_action:'publish the prepared report', stage:'prepared', state:'pending' },
      }] }],
      unavailable:[{ source_kind:'space', space:'space-a', code:'space-read-unavailable', summary:'Committed operational evidence is unavailable.' }],
    },
    spaces:[{ id:'space-a', readable:true, syncAge:'1m', stale:true }], nodes:[{ system:'atlas' }, { system:'checkout' }, { system:'billing' }],
    inbox:[first, item], outbox:[reasonless], archive:[], contracts:[], contractEdges:[], flags:[],
    unavailable:[{ id:'XW-second', scope:'space-a', title:'Item snapshot', reason:'The item source was omitted from this snapshot.', sourceClass:'unavailable' }],
    artifactDetails:[
      { ...item, sourceClass:'space', path:'exchange/XW-second.yaml', envelope:{ created:'2026-08-14T10:00:00Z' }, body:'raw body', bodyHTML:'<p>safe body</p>', events:[{ ulid:'event-note', transition:'note', actor_system:'atlas', consistency:{ actual:'submitted', claimed:'draft', event_ulid:'event-note', producer:{ kind:'a2a', version:'fixture' }, cause:'fixture receipt mismatch' } }, { ulid:'event-unknown', transition:'secret-transition-token', actor_system:'atlas' }], refs:[{ ref:'XC-one@1', resolved:true, digest_mismatch:false }], flags:[], attachments:[{ ref:'sha256:attachment' }], operational_items:item.operationalItems, sync_stale:true, sync_age:'1m' },
      { ...reasonless, sourceClass:'space', path:'exchange/XW-reasonless.yaml', envelope:{}, events:[], refs:[], flags:[], attachments:[] },
    ],
    workReports:reports, windows:{ inbox:{ shown:2, total:2, truncated:false }, outbox:{ shown:0, total:0, truncated:false }, archive:{ shown:0, total:0, truncated:false }, workReports:{ shown:2, total:2, truncated:false } },
    aggregates:{ needYou:{ items:[item], window:{ shown:1, total:4, truncated:true } }, noHumanMove:{ items:[first], window:{ shown:1, total:1, truncated:false } }, youAwait:{ items:[], window:{ shown:0, total:3, truncated:true } } },
  };
  const patches = [];
  const opened = [];
  const cardID = (kind, source) => kind === 'item'
    ? `item:${source.space}:${source.id}`
    : `work-report:${source.space}:${source.artifact_id}`;
  const actions = {
    patch(value) { patches.push(value); },
    navigate() {},
    cardID,
    cardAccent(family, identity) { return `${family}:${identity}`; },
    openCard(kind, id, accent) { opened.push({ kind, id, accent }); },
  };
  const ui = { space:'all', workTab:'all', workState:'all', workType:'all', workDue:false, workSel:'', typeMenu:false, dashboardSet:'needYou', card:null };
  const exchangeHarness = componentHarness('ExchangeView', { ctx:{ data, locale:'en', ui, actions } });
  const knownVocabulary = new Set([
    'lifecycle-state/submitted', 'outcome/open', 'gate/G3', 'operational-state/ready', 'operational-state/absent',
    'source-freshness/current', 'source-freshness/stale', 'source-freshness/degraded', 'source-freshness/unavailable', 'work-mode/waiting', 'work-mode/implementing',
    'freshness/committed-current', 'freshness/pending-recovery', 'transition/note',
  ]);
  const installResolver = harness => {
    harness.context.A2A_VOCABULARY_RESOLVER.lookup = (_data, family, value) => knownVocabulary.has(`${family}/${value}`) ? ({
      label:`${family}/${value}`, explanation:`help:${family}/${value}`, toneClass:'tone-progressing', cue:'›', cueAttribute:'›'
    }) : ({ label:'Unknown in this snapshot.', explanation:'Unknown vocabulary value.', toneClass:'tone-unknown', cue:'?', cueAttribute:'?' });
    harness.context.A2A_VOCABULARY_RESOLVER.knows = (_data, family, value) => knownVocabulary.has(`${family}/${value}`);
  };
  installResolver(exchangeHarness);
  const exchange = exchangeHarness.part.renderVals();

  const compiledFactOrder = (file, attribute) => Array.from(
    runtimeDesignPage(file).template.matchAll(new RegExp(`${attribute}="([^"]+)"`, 'g')),
    match => match[1]
  );
  const itemOrder = ['identity','lead','lifecycle','parties','gate','urgency','summary','operational','snapshot','technical'];
  // The ROW and the DETAIL are different surfaces and stopped pretending to be
  // the same one on 2026-08-19. The detail still renders every fact in P5's
  // order. The row renders the four a reader scans — what it is, its state,
  // who is handing it to whom, how fresh — because it had grown a tier per
  // fact, including five spans kept EMPTY so this assertion would not move.
  // Markup shaped by an assertion is the wrong way round; the row is now
  // shaped by the reader and this line follows it.
  const rowOrder = ['identity','lifecycle','title','parties','snapshot'];
  assert.deepEqual(compiledFactOrder('ArtifactRow.dc.html', 'data-item-row-fact'), rowOrder, 'the compiled row DOM keeps the scannable four, in order');
  assert.deepEqual(compiledFactOrder('ArtifactDetail.dc.html', 'data-item-fact'), itemOrder, 'the compiled item-card DOM must follow P5 order');
  assert.deepEqual(compiledFactOrder('ExchangeView.dc.html', 'data-work-report-fact'), ['subject','lead','attribution','checkpoint','waits','current','remaining','snapshot','technical'], 'the compiled work-report DOM must follow P5 order');

  assert.deepEqual(Array.from(exchange.workRows, row => row.id), ['XW-second'], 'aggregate drill-down must render the exact carried prefix');
  assert.equal(exchange.workWindowText, 'Showing 1 of 4.');
  assert.equal(exchange.workRows[0].reasonSentence, item.reasonSentence.en);
  assert.equal(exchange.workRows[0].waitingOn, 'checkout and billing');
  assert.equal(exchange.workRows[0].expectedMove, 'accept');
  assert.equal(exchange.workRows[0].gateResult.label, 'gate/G3');
  assert.equal(exchange.workRows[0].freshnessResult.label, 'source-freshness/unavailable', 'matching unavailable must override conflicting carried source freshness');
  assert.equal(exchange.workRows[0].terminalText, 'terminal: no');
  assert.equal(exchange.workRows[0].archivedText, 'archived: no');
  assert.equal(exchange.workRows[0].yourMoveText, 'your move: no');
  assert.equal(exchange.workRows[0].gatePendingText, 'pending: yes');
  assert.match(exchange.workRows[0].urgencyText, /overdue: no/);
  assert.equal(exchange.workRows[0].summary, item.description);
  assert.equal(exchange.workRows[0].snapshotUnavailable, 'Unavailable in this snapshot: The item source was omitted from this snapshot.');
  assert.match(exchange.workRows[0].technicalEvidence, /SyncStale: yes/);
  // P7 (dashboard-ui-restoration-2026-08 07-evidence.md, 2026-08-18
  // amendment): reasonSentence is a `when carried` fact (card-spec/item.md);
  // a missing EN sentence blank-slots rather than falling back to a Rule A
  // "not printed" sentence. Blank is still distinct from the carried RU
  // sentence ('Некоторая причина только на русском'), so this remains a
  // real guard against the RU sentence leaking into the EN render.
  assert.equal(exchangeHarness.part.rowFor(reasonless, 'workSel', '').reasonSentence, '', 'a missing EN sentence must blank-slot, not fall back to RU or print a banned empty-state sentence');
  assert.deepEqual(Array.from(exchange.workRows[0].operationalItems, row => [row.name, row.result.label]), [
    ['endpoint', 'operational-state/ready'], ['runbook', 'operational-state/absent']
  ]);
  assert.deepEqual(Array.from(exchange.workReportRows, row => row.summary), ['First carried checkpoint', 'Second carried checkpoint'], 'work reports must retain carried order');
  const legacyDetail = exchangeHarness.part.detailFor('XW-second', data.inbox);
  assert.equal(legacyDetail.syncStale, true, 'artifact-detail snapshot evidence must carry the exact sync_stale field');
  const consistencyEvent = legacyDetail.events.find(event => event.transitionRaw === 'note');
  assert.equal(consistencyEvent.hasConsistency, true);
  assert.match(consistencyEvent.consistencyText, /^authoritative actual submitted; claimed draft;/);
  assert.match(consistencyEvent.consistencyText, /event_ulid event-note; producer .*fixture.*; cause fixture receipt mismatch$/);
  // 03-interaction-model.md §2's second door: a work row selects into the
  // inline pane via setState and writes no card route. The card modal is
  // still reachable for this same item — from Overview and from a `#card=`
  // deep link — and its render stays covered by the ui.card harnesses below.
  exchange.workRows[0].select();
  assert.equal(patches.at(-1).workSel, 'XW-second', 'a work row selects into the pane, it does not open the card modal');
  assert.equal(opened.length, 0, 'the pane door must write no card route');
  exchange.workReportRows[0].open();
  assert.deepEqual(opened.pop(), { kind:'work-report', id:'work-report:space-a:XA-carried-first', accent:'checkpoint:space-a,XA-carried-first' });
  exchange.workTabs.find(tab => tab.label === 'Incoming').go();
  assert.equal(patches.at(-1).dashboardSet, '', 'ordinary filters must clear aggregate drill-down state');

  const reportCard = componentHarness('ExchangeView', {
    ctx:{ data, locale:'en', ui:{ card:{ kind:'work-report', id:'work-report:space-a:XA-carried-first', accent:actions.cardAccent('checkpoint', ['space-a','XA-carried-first']) } }, actions }
  });
  installResolver(reportCard);
  const reportValues = reportCard.part.renderVals();
  assert.equal(reportValues.cardWorkReport, true);
  assert.deepEqual(Array.from(reportValues.workReportCard.factOrder), ['subject','lead','attribution','checkpoint','waits','current','remaining','snapshot','technical']);
  assert.equal(reportValues.workReportCard.subject, 'XW-second');
  assert.equal(reportValues.workReportCard.modeResult.label, 'work-mode/waiting');
  assert.equal(reportValues.workReportCard.summary, 'First carried checkpoint');
  assert.deepEqual(Array.from(reportValues.workReportCard.waits, wait => wait.id), ['checkout','owner']);
  assert.equal(reportValues.workReportCard.currentDegraded, false, 'an explicit current Source must outrank an older diagnostic unavailable fact');
  assert.equal(reportValues.workReportCard.currentClaimsVisible, true);
  assert.equal(reportValues.workReportCard.freshnessResult.label, 'freshness/committed-current');
  assert.equal(reportValues.workReportCard.currentText, 'current: no; committed checkpoint present');
  assert.equal(reportValues.workReportCard.remaining, 'publish the prepared report');
  assert.equal(reportValues.workReportCard.snapshotResult.label, 'source-freshness/current');
  assert.equal(reportValues.workReportCard.snapshotUnavailable, '');
  assert.equal(reportValues.workReportCard.checkpointHighlighted, true, 'the opaque checkpoint accent must highlight its carried target');

  const workReportCardWithSources = (sources, unavailable = data.operational.unavailable) => {
    const harness = componentHarness('ExchangeView', {
      ctx:{
        data:{ ...data, operational:{ ...data.operational, sources, unavailable } },
        locale:'en',
        ui:{ card:{ kind:'work-report', id:'work-report:space-a:XA-carried-first' } },
        actions,
      },
    });
    installResolver(harness);
    return harness.part.renderVals().workReportCard;
  };
  for (const freshness of ['stale', 'degraded']) {
    const authoritativeSource = workReportCardWithSources([{ kind:'space', space:'space-a', freshness }]);
    assert.equal(authoritativeSource.currentDegraded, false, `an explicit ${freshness} Source must outrank a diagnostic unavailable fact`);
    assert.equal(authoritativeSource.currentClaimsVisible, true);
    assert.equal(authoritativeSource.snapshotResult.label, `source-freshness/${freshness}`);
    assert.equal(authoritativeSource.snapshotUnavailable, '');
  }
  const exactUnavailableSource = workReportCardWithSources([{ kind:'space', space:'space-a', freshness:'unavailable' }], []);
  assert.equal(exactUnavailableSource.currentDegraded, true);
  assert.equal(exactUnavailableSource.currentClaimsVisible, false);
  assert.equal(exactUnavailableSource.snapshotResult.label, 'source-freshness/unavailable');
  const unavailableWithoutSource = workReportCardWithSources([]);
  assert.equal(unavailableWithoutSource.currentDegraded, true);
  assert.equal(unavailableWithoutSource.currentClaimsVisible, false);
  assert.equal(unavailableWithoutSource.snapshotResult.label, 'source-freshness/unavailable');

  const itemCard = componentHarness('ArtifactDetail', {
    ctx:{ data, locale:'en', ui:{ card:{ kind:'item', id:'item:space-a:XW-second', accent:actions.cardAccent('owner', ['space-a','XW-second','checkout']) } }, actions }
  });
  installResolver(itemCard);
  const detail = itemCard.part.renderVals();
  assert.equal(detail.cardItem, true);
  assert.deepEqual(Array.from(detail.itemCard.factOrder), ['identity','lead','lifecycle','parties','gate','urgency','summary','operational','snapshot','technical']);
  assert.equal(detail.itemCard.reasonSentence, item.reasonSentence.en);
  assert.equal(detail.itemCard.stateResult.label, 'lifecycle-state/submitted');
  assert.equal(detail.itemCard.outcomeResult.label, 'outcome/open');
  assert.equal(detail.itemCard.gateResult.explanation, 'help:gate/G3');
  assert.equal(detail.itemCard.snapshotResult.label, 'source-freshness/unavailable', 'item snapshot must use the exact carried enum, not SyncStale');
  assert.equal(detail.itemCard.terminalText, 'terminal: no');
  assert.equal(detail.itemCard.archivedText, 'archived: no');
  assert.equal(detail.itemCard.yourMoveText, 'your move: no');
  assert.equal(detail.itemCard.gatePendingText, 'pending: yes');
  assert.match(detail.itemCard.urgencyText, /overdue: no/);
  assert.equal(detail.itemCard.snapshotUnavailable, 'Unavailable in this snapshot: The item source was omitted from this snapshot.');
  assert.match(detail.itemCard.technicalEvidence, /SyncStale: yes/);
  assert.equal(detail.itemCard.partiesHighlighted, true, 'the opaque owner accent must highlight its carried target');
  assert.equal(detail.itemCard.technicalWhy, item.why);

  const itemSnapshotsFor = snapshotData => {
    const rowHarness = componentHarness('ExchangeView', { ctx:{ data:snapshotData, locale:'en', ui, actions } });
    installResolver(rowHarness);
    const detailHarness = componentHarness('ArtifactDetail', {
      ctx:{ data:snapshotData, locale:'en', ui:{ card:{ kind:'item', id:'item:space-a:XW-second', accent:'' } }, actions },
    });
    installResolver(detailHarness);
    return [rowHarness.part.renderVals().workRows[0], detailHarness.part.renderVals().itemCard];
  };
  const sourceDiagnosticOnlyData = { ...data, unavailable:[] };
  const [sourceDiagnosticRow, sourceDiagnosticDetail] = itemSnapshotsFor(sourceDiagnosticOnlyData);
  assert.deepEqual(
    [sourceDiagnosticRow.freshnessResult.label, sourceDiagnosticDetail.snapshotResult.label],
    ['source-freshness/current', 'source-freshness/current'],
    'row and detail must keep exact Source current when only an older source diagnostic is present',
  );
  assert.deepEqual([sourceDiagnosticRow.snapshotUnavailable, sourceDiagnosticDetail.snapshotUnavailable], ['', '']);
  assert.match(sourceDiagnosticRow.technicalEvidence, /Committed operational evidence is unavailable/, 'the non-authoritative diagnostic remains technical evidence');
  assert.match(sourceDiagnosticDetail.technicalEvidence, /Committed operational evidence is unavailable/, 'the non-authoritative diagnostic remains technical evidence');

  const itemSpecificOnlyData = {
    ...data,
    operational:{ ...data.operational, unavailable:[] },
  };
  const [itemSpecificRow, itemSpecificDetail] = itemSnapshotsFor(itemSpecificOnlyData);
  assert.deepEqual(
    [itemSpecificRow.freshnessResult.label, itemSpecificDetail.snapshotResult.label],
    ['source-freshness/unavailable', 'source-freshness/unavailable'],
    'an item-specific unavailable fact must win without relying on a source diagnostic',
  );
  assert.deepEqual(
    [itemSpecificRow.snapshotUnavailable, itemSpecificDetail.snapshotUnavailable],
    ['Unavailable in this snapshot: The item source was omitted from this snapshot.', 'Unavailable in this snapshot: The item source was omitted from this snapshot.'],
  );

  for (const freshness of ['stale', 'degraded']) {
    const sourceData = {
      ...sourceDiagnosticOnlyData,
      operational:{ ...sourceDiagnosticOnlyData.operational, sources:[{ kind:'space', space:'space-a', freshness }] },
    };
    const [row, itemDetail] = itemSnapshotsFor(sourceData);
    assert.deepEqual(
      [row.freshnessResult.label, itemDetail.snapshotResult.label],
      [`source-freshness/${freshness}`, `source-freshness/${freshness}`],
      `row and detail must keep exact Source ${freshness} over a source diagnostic`,
    );
    assert.deepEqual([row.snapshotUnavailable, itemDetail.snapshotUnavailable], ['', '']);
  }

  const exactUnavailableItemData = {
    ...data,
    unavailable:[],
    operational:{ ...data.operational, sources:[{ kind:'space', space:'space-a', freshness:'unavailable' }], unavailable:[] },
  };
  const [exactUnavailableRow, exactUnavailableDetail] = itemSnapshotsFor(exactUnavailableItemData);
  assert.deepEqual(
    [exactUnavailableRow.freshnessResult.label, exactUnavailableDetail.snapshotResult.label],
    ['source-freshness/unavailable', 'source-freshness/unavailable'],
    'an exact unavailable Source must win without an unavailable diagnostic',
  );
  // P7 (dashboard-ui-restoration-2026-08 07-evidence.md, 2026-08-18
  // amendment): the snapshot condition is an `always`-presence fact
  // (card-spec/item.md position 9); Rule A couples `always` to the
  // fourth-condition sentence exactly, so an unavailable source with no
  // carried reason must use the bare "Unavailable in this snapshot." text,
  // never the banned "Unknown in this snapshot." (that argument — that
  // detail placement licenses a banned sentence — was the mistake this
  // phase's amendment corrects).
  assert.deepEqual(
    [exactUnavailableRow.snapshotUnavailable, exactUnavailableDetail.snapshotUnavailable],
    ['Unavailable in this snapshot.', 'Unavailable in this snapshot.'],
    'unavailable without a carried public-safe reason must use the always-legal fourth-condition sentence',
  );

  const absentSourceItemData = {
    ...sourceDiagnosticOnlyData,
    operational:{ ...sourceDiagnosticOnlyData.operational, sources:[] },
  };
  const [absentSourceRow, absentSourceDetail] = itemSnapshotsFor(absentSourceItemData);
  assert.deepEqual(
    [absentSourceRow.freshnessResult.label, absentSourceDetail.snapshotResult.label],
    ['source-freshness/unavailable', 'source-freshness/unavailable'],
    'a matching source diagnostic supplies unavailable only when Source is absent',
  );
  assert.deepEqual(
    [absentSourceRow.snapshotUnavailable, absentSourceDetail.snapshotUnavailable],
    ['Unavailable in this snapshot: Committed operational evidence is unavailable.', 'Unavailable in this snapshot: Committed operational evidence is unavailable.'],
  );

  const reasonlessCard = componentHarness('ArtifactDetail', {
    ctx:{ data, locale:'en', ui:{ card:{ kind:'item', id:'item:space-a:XW-reasonless', accent:'' } }, actions }
  });
  installResolver(reasonlessCard);
  // P7 (2026-08-18 amendment): reasonSentence is `when carried`
  // (card-spec/item.md position 2); blank-slot, not a Rule A sentence.
  assert.equal(reasonlessCard.part.renderVals().itemCard.reasonSentence, '', 'the item card must blank-slot a missing reason sentence');

  const unknownItem = { ...reasonless, space:'space-b', id:'XW-unknown', state:'secret-state-token', outcome:'secret-outcome-token', reasonSentence:{ en:'Unknown lifecycle item.', ru:'' } };
  const unknownItemData = { ...data, spaces:[...data.spaces, { id:'space-b', readable:true }], outbox:[reasonless, unknownItem], artifactDetails:[...data.artifactDetails, { ...unknownItem, events:[], refs:[], attachments:[] }], unavailable:[] };
  const unknownItemCard = componentHarness('ArtifactDetail', { ctx:{ data:unknownItemData, locale:'en', ui:{ card:{ kind:'item', id:'item:space-b:XW-unknown', accent:'' } }, actions } });
  installResolver(unknownItemCard);
  const unknownItemValues = unknownItemCard.part.renderVals().itemCard;
  assert.equal(unknownItemValues.lifecycleKnown, false);
  assert.equal(unknownItemValues.hasLifecyclePills, false);
  // P7 (2026-08-18 amendment): lifecycle-and-outcome is `when carried`
  // (card-spec/item.md position 3) — blank-slot. Snapshot condition is
  // `always` (position 9) — Rule A's fourth-condition text, never blank and
  // never the banned "Unknown in this snapshot.".
  assert.equal(unknownItemValues.lifecycleUnknownText, '');
  assert.equal(unknownItemValues.snapshotUnknownText, 'Unavailable in this snapshot.', 'a snapshot with neither freshness nor unavailable must use the always-legal fourth-condition sentence');
  const unknownItemRows = componentHarness('ExchangeView', { ctx:{ data:unknownItemData, locale:'en', ui:{ ...ui, dashboardSet:'', workTab:'outgoing' }, actions } });
  installResolver(unknownItemRows);
  const unknownRow = unknownItemRows.part.renderVals().workRows.find(row => row.id === 'XW-unknown');
  assert.equal(unknownRow.lifecycleKnown, false);
  // The row's own markup (ArtifactRow.dc.html) never renders these two
  // fields at all (specs/05-exchange.md §1 trims the row); they blank-slot
  // the same as every other dead view-model field this hatch used to guard.
  assert.equal(unknownRow.lifecycleUnknownText, '');
  assert.equal(unknownRow.snapshotUnknownText, '');

  const unknownReport = { ...reports[0], artifact_id:'XA-unknown-mode', work_id:'work:unknown', mode:'secret-mode-token' };
  const unknownReportData = { ...data, workReports:[...reports, unknownReport] };
  const unknownReportCard = componentHarness('ExchangeView', { ctx:{ data:unknownReportData, locale:'en', ui:{ card:{ kind:'work-report', id:'work-report:space-a:XA-unknown-mode', accent:'' } }, actions } });
  installResolver(unknownReportCard);
  const unknownReportValues = unknownReportCard.part.renderVals().workReportCard;
  assert.equal(unknownReportValues.modeKnown, false);
  // P7 (2026-08-18 amendment): the lead sentence is `when carried`
  // (card-spec/work-report.md position 2) — blank-slot rather than the
  // banned "Unknown in this snapshot.". The raw enum token must still never
  // leak into it.
  assert.equal(unknownReportValues.lead, '');
  assert.doesNotMatch(unknownReportValues.lead, /secret-mode-token/);

  const eventDetail = exchangeHarness.part.detailFor('XW-second', data.inbox);
  const lifecycleEvents = eventDetail.events.filter(event => event.isLifecycle);
  // P7 (2026-08-18 amendment): an unrecognized transition is "value cannot
  // be established" — blank-slot, not a printed sentence.
  assert.deepEqual(Array.from(lifecycleEvents, event => [event.transitionRaw, event.actionText]), [
    ['note', 'transition/note'],
    ['secret-transition-token', ''],
  ]);
  assert.doesNotMatch(lifecycleEvents[1].actionText, /secret-transition-token/, 'an unknown transition must not leak its raw enum into visible history');

  const renderItemAccent = (family, identity) => {
    const harness = componentHarness('ArtifactDetail', { ctx:{ data, locale:'en', ui:{ card:{ kind:'item', id:'item:space-a:XW-second', accent:actions.cardAccent(family, identity) } }, actions } });
    installResolver(harness);
    return harness.part.renderVals().itemCard;
  };
  const itemAccentCases = [
    ['move', ['space-a','XW-second','accept'], card => card.partiesHighlighted],
    ['event', ['space-a','XW-second','event-note'], card => card.technicalTargets.find(target => target.family === 'event' && target.identity === 'event-note')?.highlighted],
    ['reference', ['space-a','XW-second','XC-one@1'], card => card.technicalTargets.find(target => target.family === 'reference' && target.identity === 'XC-one@1')?.highlighted],
    ['attachment', ['space-a','XW-second','sha256:attachment'], card => card.technicalTargets.find(target => target.family === 'attachment' && target.identity === 'sha256:attachment')?.highlighted],
    ['operational-item', ['space-a','XW-second','endpoint'], card => card.operationalItems[0].highlighted],
  ];
  for (const [family, identity, isHighlighted] of itemAccentCases) {
    const accented = renderItemAccent(family, identity);
    assert.equal(isHighlighted(accented), true, `${family} accent must highlight its exact target`);
    assert.deepEqual(Array.from(accented.factOrder), Array.from(detail.itemCard.factOrder));
    assert.equal(accented.reasonSentence, detail.itemCard.reasonSentence, `${family} accent must not change item content`);
  }
  const eventAccent = renderItemAccent('event', ['space-a','XW-second','event-note']);
  assert.deepEqual(Array.from(eventAccent.technicalTargets, target => [target.family, target.identity, target.highlighted]), [
    ['event', 'event-note', true],
    ['event', 'event-unknown', false],
    ['reference', 'XC-one@1', false],
    ['attachment', 'sha256:attachment', false],
  ], 'event/reference/attachment accents must address distinct rendered identity rows');

  const renderReportAccent = (family, identity) => {
    const harness = componentHarness('ExchangeView', { ctx:{ data, locale:'en', ui:{ card:{ kind:'work-report', id:'work-report:space-a:XA-carried-first', accent:actions.cardAccent(family, identity) } }, actions } });
    installResolver(harness);
    return harness.part.renderVals().workReportCard;
  };
  const reportAccentCases = [
    ['subject', ['space-a','XA-carried-first','XW-second'], card => card.subjectHighlighted],
    ['actor', ['space-a','XA-carried-first','agent','codex','atlas'], card => card.actorHighlighted],
    ['wait', ['space-a','XA-carried-first','system','checkout'], card => card.waits[0].highlighted],
  ];
  for (const [family, identity, isHighlighted] of reportAccentCases) {
    const accented = renderReportAccent(family, identity);
    assert.equal(isHighlighted(accented), true, `${family} accent must highlight its exact target`);
    assert.deepEqual(Array.from(accented.factOrder), Array.from(reportValues.workReportCard.factOrder));
    assert.equal(accented.lead, reportValues.workReportCard.lead, `${family} accent must not change report content`);
  }

  const emptyAggregateHarness = componentHarness('ExchangeView', {
    ctx:{ data, locale:'en', ui:{ ...ui, dashboardSet:'youAwait' }, actions }
  });
  installResolver(emptyAggregateHarness);
  const emptyAggregate = emptyAggregateHarness.part.renderVals();
  assert.deepEqual(Array.from(emptyAggregate.workRows), []);
  assert.equal(emptyAggregate.workWindowText, 'Showing 0 of 3.');
  assert.match(emptyAggregate.emptyWorkText, /bounded prefix/i, 'an empty bounded prefix must not claim the whole collection is empty');

  const ordinaryEmptyData = {
    ...data,
    inbox:[], outbox:[], archive:[],
    workReports:[],
    windows:{
      ...data.windows,
      inbox:{ shown:0, total:5, truncated:true },
      outbox:{ shown:0, total:6, truncated:true },
      archive:{ shown:0, total:7, truncated:true },
      workReports:{ shown:0, total:2, truncated:true },
    },
  };
  let ordinaryEmpty;
  // Direction is a selection across the collections now, so a direction has no
  // window of its own. The notice names the bounds the server DID send for the
  // collections that direction draws from — an archived document may be either
  // direction, so its bound belongs to both — and never adds them into a union
  // total nobody computed. "archive" is no longer a direction at all.
  for (const [workTab, expected] of [
    ['all', 'This snapshot is bounded: 0 of 5 incoming; 0 of 6 outgoing; 0 of 7 archived.'],
    ['incoming', 'This snapshot is bounded: 0 of 5 incoming; 0 of 7 archived.'],
    ['outgoing', 'This snapshot is bounded: 0 of 6 outgoing; 0 of 7 archived.'],
  ]) {
    const ordinaryEmptyHarness = componentHarness('ExchangeView', {
      ctx:{ data:ordinaryEmptyData, locale:'en', ui:{ ...ui, dashboardSet:'', workTab }, actions }
    });
    installResolver(ordinaryEmptyHarness);
    const values = ordinaryEmptyHarness.part.renderVals();
    ordinaryEmpty ||= values;
    assert.equal(values.workWindowText, expected, `${workTab} names its collections' carried bounds`);
    assert.match(values.emptyWorkText, /bounded prefix/i, `${workTab} must keep empty-prefix semantics`);
    assert.doesNotMatch(values.emptyWorkTitle + values.emptyWorkText, /collection is empty/i);
  }
  assert.equal(ordinaryEmpty.hasWorkReports, false);
  assert.equal(ordinaryEmpty.workReportsWindowText, 'Showing 0 of 2.');
  assert.equal(ordinaryEmpty.workReportsEmpty, true);
  assert.match(ordinaryEmpty.workReportsEmptyText, /bounded prefix/i, 'an empty truncated report prefix must remain visible and honest');

  const knownEmptyReportsData = {
    ...ordinaryEmptyData,
    windows:{ ...ordinaryEmptyData.windows, workReports:{ shown:0, total:0, truncated:false } },
  };
  const knownEmptyReportsHarness = componentHarness('ExchangeView', {
    ctx:{ data:knownEmptyReportsData, locale:'en', ui:{ ...ui, dashboardSet:'', workTab:'incoming' }, actions }
  });
  installResolver(knownEmptyReportsHarness);
  // P7 (2026-08-18 amendment): a confirmed-empty (total:0) window is Rule
  // A's "empty collection known complete" condition — banned outside
  // technical the same as the other two, so it blank-slots rather than
  // printing "None recorded.".
  assert.equal(knownEmptyReportsHarness.part.renderVals().workReportsEmptyText, '');

  const exchangeSource = component('ExchangeView');
  assert.doesNotMatch(exchangeSource, /const STATE_TONE|blockedWhy\(|statusOf\(|const isClosed|byNewest|statuses\s*:|urgent\s*:|EVENT_SIGNAL|TRANSITION_PAST|VERDICT_TONE|VERDICT_LABEL|WORK_WAIT_KIND|workEvents[\s\S]{0,220}\.sort\(/);
  assert.doesNotMatch(exchangeSource, /\.sort\(|source-freshness[^\n]*(?:syncStale|\.stale)|(?:syncStale|\.stale)[^\n]*source-freshness/);
  assert.doesNotMatch(exchangeSource, /artifactSourceURL|git@github\.com|ssh:\/\/git@github\.com|revision\s*\|\|\s*["']HEAD["']/, 'Exchange must use only the carried root sourceURL seam and an exact revision');
  assert.match(exchangeSource, /find\(work => work\.work_id === report\.work_id\)/, 'Work.Current must join only on exact carried work identity');
  assert.doesNotMatch(exchangeSource, /find\(work =>[^\n]*committed_checkpoint[^\n]*report\.artifact_id/, 'checkpoint artifact identity must not join Work.Current');
  assert.match(exchangeSource, /\.knows\(d, "work-mode", report\.mode\)/);
  const rowSource = component('ArtifactRow');
  assert.doesNotMatch(rowSource, /syncStale|statusWhy|p\.tone|urgent|FreshnessDot[^\n]*stale=/);
  const detailSource = component('ArtifactDetail');
  assert.doesNotMatch(detailSource, /\bmodal\b|onClose|position:fixed|aria-modal|shellRole|dismissWide|document\.body\.style\.overflow|addEventListener\("keydown"/);
  assert.doesNotMatch(detailSource, /FreshnessDot[^\n]*stale=|source-freshness[^\n]*syncStale|syncStale[^\n]*source-freshness|calloutTone|pillTone|VERDICT_TONE|VERDICT_LABEL/);
  assert.match(detailSource, /\.knows\(data, "lifecycle-state", item\.state\)/);
  assert.match(detailSource, /data-item-technical-target=/, 'item technical identities must have distinct rendered accent targets');
  assert.doesNotMatch(detailSource, /technicalHighlighted|metadataCount/, 'the generic technical accent and browser-derived metadata count must be absent');
  assert.doesNotMatch(exchangeSource, /<sc-if value="\{\{ hasWorkReports \}\}"[^>]*>\s*<section data-work-report-list/, 'the report section must not disappear with an empty carried prefix');
});

// P1 shipped this against a defect where SegmentedFilter printed `0` for a
// count nobody passed. That invariant stands and is asserted below.
//
// What changed on 2026-08-19: direction became a SELECTION across the three
// server collections (the v0.22.0 model — a closed incoming document stays
// incoming instead of moving to an archive-shaped third bucket). A direction
// therefore has no single carried window to report, so a chip counts the
// documents it will show, and the bound is stated in the window sentence
// beneath the chips rather than folded into the number.
//
// The two are different claims and only one of them was ever P1's: "never
// print a number nobody computed" is kept; "the chip equals windows.<k>.total"
// could not survive a chip that spans collections. A truncated collection now
// under-reports in the chip and says so in the sentence — which is the honest
// pair, not a silent one.
test('P1 Exchange facet chips count what they will show, and never an invented zero', () => {
  const mkItem = (space, id) => ({ id, space, type: 'work_request', title: id, from: 'atlas', to: ['checkout'], state: 'submitted' });
  const nodes = [{ system: 'atlas', owners: ['atlas'] }, { system: 'checkout', owners: ['checkout'] }];
  const spaces = [{ id: 'space-a', readable: true }, { id: 'space-b', readable: true }];
  const data = {
    self: 'atlas', nodes, spaces, unavailable: [], operational: { sources: [] }, workReports: [],
    contracts: [], contractEdges: [], flags: [],
    inbox: [mkItem('space-a', 'XW-1'), mkItem('space-a', 'XW-2'), mkItem('space-b', 'XW-3')],
    outbox: [],
    archive: [mkItem('space-a', 'XW-4')],
    // outbox is genuinely empty (window total 0); archive's window total (13) is
    // far larger than its one-item carried array — a truncated collection, and
    // the chip must report the window's total, not the shorter shown array.
    windows: {
      inbox: { shown: 3, total: 3, truncated: false },
      outbox: { shown: 0, total: 0, truncated: false },
      archive: { shown: 1, total: 13, truncated: true },
    },
  };
  const actions = {};

  const allSpaces = componentHarness('ExchangeView', {
    ctx: { data, locale: 'en', ui: { space: 'all', workTab: 'incoming', workSel: '', dashboardSet: '' }, actions },
  }).part.renderVals();
  const tab = label => allSpaces.workTabs.find(t => t.label === label);
  // This fixture files every item as from:'atlas' to:['checkout'] while
  // self is 'atlas', so by direction every one of them is OUTGOING —
  // including the three parked in the inbox array. That was invisible while
  // direction WAS the array it sat in; it is the point now.
  assert.equal(tab('All').count, 4, 'All spans the three collections');
  assert.equal(tab('Incoming').count, 0, 'nothing here is addressed to self, so Incoming is truly 0 — a counted zero, not an unpassed one');
  assert.equal(tab('Outgoing').count, 4, 'every item is from self, across inbox and archive alike');
  assert.notEqual(tab('Incoming').count, undefined, 'the count is always computed; an absent count is the P1 defect');
  assert.notEqual(tab('Outgoing').count, undefined, 'the count is always computed; an absent count is the P1 defect');
  // The harness asks for the Incoming direction, and nothing here is addressed
  // to self — so an empty list is the correct answer, and the chip agrees with
  // it. Rows and chip disagreeing is the defect worth catching.
  assert.equal(allSpaces.workRows.length, 0, 'rows agree with the Incoming chip');
  assert.equal(allSpaces.workRows.length, tab('Incoming').count, 'the rendered rows and the active chip must always agree');

  const scoped = componentHarness('ExchangeView', {
    ctx: { data, locale: 'en', ui: { space: 'space-a', workTab: 'incoming', workSel: '', dashboardSet: '' }, actions },
  }).part.renderVals();
  const scopedTab = label => scoped.workTabs.find(t => t.label === label);
  assert.equal(scopedTab('Outgoing').count, 3, 'a narrowed space filter narrows the chip too');
  assert.equal(scoped.workRows.length, scopedTab('Incoming').count, 'rows and the active chip agree under a space filter too');
});

test('artifact detail suppresses the carried work report publish lifecycle duplicate', () => {
  const controller = dashboardController();
  const data = exchangeSubjectFixture();
  const carrier = data.artifactDetails.find(artifact => artifact.id === 'XA-report-artifact');
  carrier.events = [
    { transition: 'publish', at: '2026-08-04T11:00:00Z', actor_system: 'atlas' },
    { transition: 'note', at: '2026-08-04T11:01:00Z', actor_system: 'atlas' },
  ];
  controller.state.data = data;

  const detail = controller.detailFor(carrier.id, data.inbox);
  assert.deepEqual(Array.from(detail.events, event => [event.isWorkReport, event.actionText]), [
    [true, 'reported work on'],
    [false, 'note added'],
  ]);
});

test('Exchange surfaces and routes every canonical work-report subject kind', () => {
  const controller = dashboardController();
  const data = exchangeSubjectFixture();
  controller.state.data = data;

  const cases = [
    {
      report: 'XA-report-artifact', label: 'Target artifact',
      assertRoute: () => {
        assert.equal(controller.state.view, 'work');
        assert.equal(controller.state.space, 'space-a');
        assert.equal(controller.state.workSel, 'XW-target');
      },
    },
    {
      report: 'XA-report-thread', label: 'Shared thread',
      assertRoute: () => {
        assert.equal(controller.state.view, 'threads');
        assert.equal(controller.state.space, 'space-a');
        assert.equal(controller.state.threadSel, 'thread:shared');
        assert.equal(controller.state.threadSpace, 'space-a');
      },
    },
    {
      report: 'XA-report-contract', label: 'XC-orders@2.4.0',
      assertRoute: () => {
        assert.equal(controller.state.view, 'contracts');
        assert.equal(controller.state.space, 'space-a');
        assert.equal(controller.state.conSel, 'XC-orders');
        assert.equal(controller.state.conVersion, '2.4.0');
      },
    },
  ];

  for (const matrixCase of cases) {
    controller.state.view = 'work';
    const detail = controller.detailFor(matrixCase.report, data.inbox);
    assert.equal(detail.events.length, 1, `${matrixCase.report} must expose its committed report`);
    assert.equal(detail.events[0].subjectLabel, matrixCase.label);
    assert.equal(detail.events[0].hasSubjectLink, true);
    detail.events[0].openSubject();
    matrixCase.assertRoute();
  }
});

test('all-spaces thread selection uses the exact space and thread identity', () => {
  const controller = dashboardController();
  const data = JSON.parse(repoFile('internal/html/testdata/demo.json'));
  const first = structuredClone(data.threads[0]);
  const firstView = structuredClone(data.threadViews.find(view => view.thread === first.id && view.space === first.space));
  const secondSpace = data.spaces.find(space => space.id !== first.space).id;
  const second = structuredClone(first);
  second.space = secondSpace;
  second.opener.title = 'Duplicate thread in second space';
  const secondView = structuredClone(firstView);
  secondView.space = secondSpace;
  secondView.opener.title = 'Duplicate thread in second space';
  data.threads = [first, second];
  data.threadViews = [firstView, secondView];
  data.workReports = [];
  controller.state.data = data;
  controller.state.view = 'threads';
  controller.state.space = 'all';

  let values = controller.renderVals();
  assert.deepEqual(Array.from(values.threadRows, row => row.selected), [true, false]);
  const firstID = controller.cardID('thread', { space:first.space, thread:first.id });
  const secondID = controller.cardID('thread', { space:secondSpace, thread:first.id });
  assert.notEqual(firstID, secondID, 'space is part of the opaque thread identity');
  values.threadRows[1].openCard();
  assert.deepEqual(JSON.parse(JSON.stringify(controller.state.card)), { kind:'thread', id:secondID, accent:'' });

  // Modal's card context contains ui.card only. Keep this explicit: a stale
  // background space filter must never erase the exact opaque selection.
  values = controller.threadsValues({ card:controller.state.card, space:undefined });
  assert.equal(values.cardMode, true);
  assert.equal(values.tvSpaceText, secondSpace);
  assert.equal(values.tvTitle, 'Duplicate thread in second space');
});

test('Guide keeps the new operational and exact-contract cards in EN/RU parity', () => {
  const controller = dashboardController();
  controller.state.locale = 'en';
  const english = controller.renderVals().guideFeatures;
  controller.state.locale = 'ru';
  const russian = controller.renderVals().guideFeatures;

  assert.equal(english.length, russian.length);

  // Cards are found BY TAG, not by position. They used to be pinned as
  // slice(-2), which made the assertion fail the moment a card was inserted
  // ahead of them — a true statement about the catalogue reported as a
  // regression in cards that had not changed. The tag is the identity; where
  // it sits in the list is not what this test is about.
  const cardsByTag = (cards) => new Map(cards.map((card) => [card.tag, card]));
  const en = cardsByTag(english);
  const ru = cardsByTag(russian);
  for (const tag of ['work report', 'id@version', 'outcome · terminal']) {
    assert.ok(en.has(tag), `EN Guide is missing the ${tag} card`);
    assert.ok(ru.has(tag), `RU Guide is missing the ${tag} card`);
  }

  // Parity is ORDER parity too, which the positional version could not see:
  // a card appended in one language and inserted in the other left both
  // slice(-2) assertions passing while the two Guides listed different things
  // in different places. That happened while this card was being added.
  assert.deepEqual(
    Array.from(english, (card) => card.tag),
    Array.from(russian, (card) => card.tag),
    'EN and RU Guides must list the same cards in the same order'
  );

  assert.match(en.get('work report').body, /unknown, never idle/);
  assert.match(ru.get('work report').body, /«неизвестно».*«простой»/);
  assert.match(en.get('id@version').body, /immutable carried set.*preflight, materialize, and check/);
  assert.match(ru.get('id@version').body, /неизменяемый набор файлов.*preflight, materialize и check/);
  // The release headline: outcome and terminal are DIFFERENT questions, and
  // the card has to say so rather than treating them as one flag.
  assert.match(en.get('outcome · terminal').body, /refused AND non-terminal/);
  assert.match(ru.get('outcome · terminal').body, /отклонено и при этом не терминально/);
});

test('P12 contract cards preserve exact version packages and honest file bytes', () => {
  const retiredDetail = {
    status:'available', description:'Retired package description', changeSummary:'Retired-only change summary',
    publishedAt:'2025-11-18T10:20:00Z', publishedBy:'atlas', schemaFormat:'json-schema-draft-07', compatPolicy:'frozen',
    consumerPins:[
      { system:'fulfillment', space:'space-a', pinnedMajor:1, pinnedVersion:'1.5.0', pinnedState:'retired', drift:'retired' },
      { system:'ledger', space:'space-a', pinnedMajor:1, pinnedVersion:'1.5.0', pinnedState:'deprecated', drift:'behind' },
    ],
    totalDocumentCount:5, omittedDocumentCount:2,
    documents:[
      { path:'contracts/XC-orders/1.5.0/retirement.md', role:'migration-guide', normative:false, mediaType:'text/markdown', sizeBytes:380, preview:'retired preview', previewLanguage:'markdown', shownBytes:64, totalBytes:380, truncated:true, digest:'retired-digest' },
      { path:'contracts/XC-orders/1.5.0/schema.json', role:'schema', normative:true, mediaType:'application/schema+json', sizeBytes:100, preview:'full preview', previewLanguage:'json', shownBytes:100, totalBytes:100, truncated:false, digest:'old-schema-digest' },
      { path:'contracts/XC-orders/1.5.0/empty.txt', role:'supporting', normative:false, mediaType:'text/plain', sizeBytes:0, preview:'', previewLanguage:'text', shownBytes:0, totalBytes:0, truncated:false, digest:'empty-digest' },
    ],
    history:[
      { eventID:'event-publish', transition:'publish', state:'published', actor:'atlas', at:'2025-11-18T10:20:00Z' },
      { eventID:'event-deprecate', transition:'deprecate', state:'deprecated', actor:'atlas', at:'2026-07-01T09:00:00Z' },
      { eventID:'event-retire', transition:'<raw-retire-transition>', state:'retired', actor:'atlas', at:'2026-07-30T12:00:00Z' },
    ],
    provenance:{ profile:'contract-tree-v1', commitSHA:'retired-commit', treeObjectID:'retired-tree', publishedDigest:'retired-package-digest' },
  };
  const currentDetail = {
    status:'available', description:'Current package description', changeSummary:'Current-only change summary',
    publishedAt:'2026-07-22T13:05:00Z', publishedBy:'atlas', schemaFormat:'json-schema-2020-12', compatPolicy:'backward',
    consumerPins:[], totalDocumentCount:1, omittedDocumentCount:0,
    documents:[{ path:'contracts/XC-orders/2.2.0/current.json', role:'schema', normative:true, mediaType:'application/schema+json', sizeBytes:20, preview:'current preview', previewLanguage:'json', shownBytes:20, totalBytes:20, truncated:false, digest:'current-digest' }],
    history:[{ eventID:'event-current', transition:'publish', state:'published', actor:'atlas', at:'2026-07-22T13:05:00Z' }],
    provenance:{ profile:'contract-set-v2', commitSHA:'current-commit' },
  };
  const contract = {
    space:'space-a', id:'XC-orders', provider:'atlas', version:'2.2.0', state:'published', description:'Contract overview',
    consumers:['checkout','fulfillment'], codeBacked:true, category:'events', schemaFormat:'json-schema-2020-12', compatPolicy:'backward',
    versions:[
      { version:'1.5.0', state:'retired', sunset:'2026-07-31', successor:'XC-orders@2.2.0', deprecationID:'XA-retire', detail:retiredDetail },
      { version:'2.2.0', state:'published', detail:currentDetail },
    ],
  };
  const emptyContract = {
    space:'space-b', id:'XC-empty', provider:'atlas', version:'0.1.0', state:'published', description:'',
    consumers:[], codeBacked:false, category:'', schemaFormat:'', compatPolicy:'', versions:[],
  };
  const unknownDetail = {
    status:'available', description:'Unknown-state package', changeSummary:'', publishedAt:'', publishedBy:'', schemaFormat:'', compatPolicy:'',
    consumerPins:[], totalDocumentCount:0, omittedDocumentCount:0, documents:[], history:[], provenance:{},
  };
  const unknownContract = {
    space:'space-a', id:'XC-unknown', provider:'atlas', version:'9.9.9', state:'<raw-contract-state>', description:'Unknown contract state',
    consumers:[], codeBacked:false, versions:[{ version:'9.9.9', state:'<raw-version-state>', detail:unknownDetail }],
  };
  const unavailableContract = {
    space:'space-b', id:'XC-unavailable', provider:'atlas', version:'0.9.0', state:'published', description:'Unavailable package',
    consumers:[], codeBacked:false, versions:[{ version:'0.9.0', state:'published', detail:{ status:'unavailable', unavailableReason:'Package history is unavailable' } }],
  };
  const edges = [
    { from:'checkout', to:'atlas', space:'space-a', contract:'XC-orders', pinnedMajor:2, pinnedVersion:'2.2.0', pinnedState:'published', providerVersion:'2.2.0', drift:'current' },
    { from:'fulfillment', to:'atlas', space:'space-a', contract:'XC-orders', pinnedMajor:1, pinnedVersion:'1.5.0', pinnedState:'retired', providerVersion:'2.2.0', state:'published', drift:'retired', successor:'XC-orders@2.2.0' },
  ];
  const aggregateItems = [edges[1], edges[0]];
  const degradations = [
    { space:'space-b', system:'catalog', path:'consumes.yaml', code:'dependency-read-failed', reason:'carried reason one' },
    { space:'space-c', system:'billing', path:'deps.yaml', code:'dependency-schema-invalid', reason:'carried reason two' },
  ];
  const data = {
    self:'atlas', generatedAt:'2026-08-14T12:00:00Z', vocabulary:{ entries:[], unknown:{} },
    spaces:[{ id:'space-a', repoURL:'https://github.com/acme/space-a.git', revision:'revision-123', readable:true }],
    operational:{
      sources:[{ kind:'space', space:'space-a', freshness:'stale', revision:'revision-123' }],
      unavailable:[
        { source_kind:'space', space:'space-a', code:'space-read-unavailable', summary:'Older unavailable evidence must not override the carried source.' },
        { source_kind:'space', space:'space-b', code:'space-read-unavailable', summary:'Committed operational evidence is unavailable.' },
      ],
    },
    contracts:[contract,emptyContract,unknownContract,unavailableContract], contractEdges:edges, flags:[], inbox:[], outbox:[], archive:[], nodes:[], unavailable:[],
    aggregates:{ linesOffCurrent:{ items:aggregateItems, window:{ shown:2, total:9, truncated:true }, complete:false, degradations } },
  };
  const opened = [];
  const copied = [];
  const cardID = (kind, source) => kind === 'contract'
    ? `opaque:${kind}:${source.space}:${source.id}`
    : kind === 'cfile'
      ? `opaque:${kind}:${source.space}:${source.id}:${source.version}:${source.path}`
      : `opaque:${kind}:${source.space}:${source.id}:${source.version}`;
  const cardAccent = (family, identity) => `opaque-accent:${family}:${JSON.stringify(identity)}`;
  const actions = {
    patch() {}, navigate() {}, cardID, cardAccent,
    openCard(kind, id, accent) { opened.push({ kind, id, accent }); },
    copy(value) { copied.push(value); },
    sourceURL(space, path, revision) { return space === 'space-a' && revision === 'revision-123' ? `https://github.com/acme/space-a/blob/${revision}/${path}` : ''; },
  };
  const knownVocabulary = new Set([
    'lifecycle-state/published', 'lifecycle-state/deprecated', 'lifecycle-state/retired',
    'dependency-drift/current', 'dependency-drift/behind', 'dependency-drift/retired',
    'source-freshness/stale', 'source-freshness/unavailable',
    'transition/publish', 'transition/deprecate', 'transition/<raw-retire-transition>',
  ]);
  const transitionLabels = {
    'transition/publish':{ en:'Published transition', ru:'Публикация' },
    'transition/deprecate':{ en:'Deprecated transition', ru:'Объявление устаревшей' },
    'transition/<raw-retire-transition>':{ en:'Retired transition', ru:'Вывод версии' },
  };
  const lookup = (_data, family, value, locale) => ({
    label:transitionLabels[`${family}/${value}`]?.[locale] || (knownVocabulary.has(`${family}/${value}`) ? `${family}/${value}` : 'Unknown value'),
    explanation:family === 'transition' ? (locale === 'ru' ? 'Справка о переходе' : 'Transition help') : `help:${family}/${value}`,
    toneClass:'tone-settled', cue:'✓', cueAttribute:'✓'
  });
  const knows = (_data, family, value) => knownVocabulary.has(`${family}/${value}`);
  const render = (locale, ui) => {
    const harness = componentHarness('ContractsView', { ctx:{ data, locale, ui, actions } });
    harness.context.A2A_VOCABULARY_RESOLVER.lookup = lookup;
    harness.context.A2A_VOCABULARY_RESOLVER.knows = knows;
    return harness.part.renderVals();
  };

  const aggregate = render('en', { space:'all', dashboardSet:'linesOffCurrent', card:null });
  assert.deepEqual(Array.from(aggregate.aggregateLineRows, row => row.edge), aggregateItems, 'aggregate drill-down must preserve the exact carried edge prefix and order');
  assert.equal(aggregate.aggregateWindow, data.aggregates.linesOffCurrent.window, 'aggregate bounds must be the carried window object');
  assert.equal(aggregate.aggregateWindowText, 'Showing 2 of 9.');
  assert.deepEqual(Array.from(aggregate.aggregateDegradations, row => row.source), degradations, 'aggregate degradations must preserve carried order');
  assert.equal(aggregate.aggregateComplete, false);

  const contractAccent = cardAccent('version', ['space-a','XC-orders','1.5.0']);
  const overview = render('en', { card:{ kind:'contract', id:cardID('contract', contract), accent:contractAccent } });
  assert.equal(overview.cardContract, true);
  assert.equal(overview.cardContractVersion, false);
  assert.deepEqual(Array.from(overview.contractCard.versions, row => row.version), ['1.5.0','2.2.0'], 'version rows must retain carried order');
  assert.equal(overview.contractCard.versions[0].stateResult.label, 'lifecycle-state/retired');
  assert.equal(overview.contractCard.versions[0].highlighted, true);
  assert.equal(overview.contractCard.dependencies[1].driftResult.explanation, 'help:dependency-drift/retired');
  assert.equal(overview.contractCard.dependencies[1].pinnedStateResult.label, 'lifecycle-state/retired');
  assert.equal(overview.contractCard.dependencies[1].providerStateResult.label, 'lifecycle-state/published');
  assert.equal(overview.contractCard.snapshotResult.label, 'source-freshness/stale');
  assert.notEqual(overview.contractCard.snapshotResult.label, 'source-freshness/unavailable', 'a carried source freshness result takes precedence over an older unavailable record');
  overview.contractCard.dependencies[1].openSuccessor();
  assert.deepEqual(opened.pop(), { kind:'cver', id:cardID('cver', { space:'space-a', id:'XC-orders', version:'2.2.0' }), accent:'' });
  overview.contractCard.versions[0].open();
  assert.deepEqual(opened.pop(), { kind:'cver', id:cardID('cver', { space:'space-a', id:'XC-orders', version:'1.5.0' }), accent:'' });
  const consumerOverview = render('en', { card:{ kind:'contract', id:cardID('contract', contract), accent:cardAccent('consumer', ['space-a','XC-orders','checkout']) } });
  assert.equal(consumerOverview.contractCard.consumers[0].highlighted, true);
  const dependencyOverview = render('en', { card:{ kind:'contract', id:cardID('contract', contract), accent:cardAccent('dependency-line', ['space-a','fulfillment','atlas','XC-orders']) } });
  assert.equal(dependencyOverview.contractCard.dependencies[1].highlighted, true);

  const documentAccent = cardAccent('document', ['space-a','XC-orders','1.5.0',retiredDetail.documents[0].path]);
  const retired = render('en', { card:{ kind:'cver', id:cardID('cver', { space:'space-a', id:'XC-orders', version:'1.5.0' }), accent:documentAccent } });
  assert.equal(retired.cardContract, false);
  assert.equal(retired.cardContractVersion, true);
  assert.equal(retired.contractVersionCard.detail, retiredDetail, 'the exact retired detail object must be selected atomically');
  assert.equal(retired.contractVersionCard.description, 'Retired package description');
  assert.equal(retired.contractVersionCard.changeSummary, 'Retired-only change summary');
  assert.equal(retired.contractVersionCard.provenance.commitSHA, 'retired-commit');
  assert.deepEqual(Array.from(retired.contractVersionCard.documents, row => row.path), retiredDetail.documents.map(document => document.path), 'retired documents must not fall back to the current package or change order');
  assert.deepEqual(Array.from(retired.contractVersionCard.history, row => row.eventID), ['event-publish','event-deprecate','event-retire'], 'history must retain carried order');
  assert.equal(retired.contractVersionCard.stateResult.label, 'lifecycle-state/retired');
  assert.equal(retired.contractVersionCard.consumerPins[0].driftResult.label, 'dependency-drift/retired');
  assert.equal(retired.contractVersionCard.consumerPins[0].pinnedStateResult.label, 'lifecycle-state/retired');
  assert.equal(retired.contractVersionCard.history[1].stateResult.label, 'lifecycle-state/deprecated');
  assert.equal(retired.contractVersionCard.history[2].transitionResult.label, 'Retired transition');
  assert.equal(retired.contractVersionCard.history[2].transition, 'Retired transition');
  assert.doesNotMatch(JSON.stringify(retired.contractVersionCard.history), /<raw-retire-transition>/, 'raw transition enums must not leak through the card');
  assert.equal(retired.contractVersionCard.documents[0].byteNotice, 'Showing the first 64 bytes of 380 bytes.');
  assert.equal(retired.contractVersionCard.documents[1].byteNotice, '', 'complete previews must not display a truncation notice');
  assert.equal(retired.contractVersionCard.documents[2].sizeText, '0 bytes', 'a zero-byte document is a known exact size, not a missing fact');
  assert.equal(retired.contractVersionCard.documentsWindowText, 'Showing 3 of 5.');
  retiredDetail.totalDocumentCount = 3;
  const fullyShownDocuments = render('en', { card:{ kind:'cver', id:cardID('cver', { space:'space-a', id:'XC-orders', version:'1.5.0' }), accent:'' } });
  assert.equal(fullyShownDocuments.contractVersionCard.documentsWindowText, '', 'an equal shown/total inventory must not claim truncation');
  retiredDetail.totalDocumentCount = 5;
  retiredDetail.omittedDocumentCount = 0;
  const nonTruncatedDocuments = render('en', { card:{ kind:'cver', id:cardID('cver', { space:'space-a', id:'XC-orders', version:'1.5.0' }), accent:'' } });
  assert.equal(nonTruncatedDocuments.contractVersionCard.documentsWindowText, '', 'a non-truncated document inventory must not render bounds');
  retiredDetail.omittedDocumentCount = 2;
  assert.equal(retired.contractVersionCard.documents[0].highlighted, true);
  assert.equal(retired.contractVersionCard.documents[0].cfileKind, 'cfile');
  assert.equal(retired.contractVersionCard.documents[0].cfileID, cardID('cfile', { space:'space-a', id:'XC-orders', version:'1.5.0', path:retiredDetail.documents[0].path }));
  assert.notEqual(retired.contractVersionCard.documents[0].cfileID, retired.contractVersionCard.documents[0].accent, 'reserved cfile identity and document emphasis are separate opaque channels');
  assert.deepEqual(Array.from(retired.contractVersionCard.documents[0].actions, action => action.kind), ['copy','source'], 'file rows keep only copy-command and open-source actions');
  retired.contractVersionCard.documents[0].actions[0].run();
  assert.equal(copied.pop(), 'a2a contract materialize XC-orders@1.5.0 --to <project-relative-dir>');
  assert.equal(retired.contractVersionCard.documents[0].actions[1].url, 'https://github.com/acme/space-a/blob/revision-123/contracts/XC-orders/1.5.0/retirement.md');

  const pinAccent = render('en', { card:{ kind:'cver', id:cardID('cver', { space:'space-a', id:'XC-orders', version:'1.5.0' }), accent:cardAccent('consumer-pin', ['space-a','XC-orders','1.5.0','fulfillment']) } });
  assert.equal(pinAccent.contractVersionCard.consumerPins[0].highlighted, true);
  assert.deepEqual(Array.from(pinAccent.contractVersionCard.documents, row => row.path), retiredDetail.documents.map(document => document.path), 'an accent must not change exact-version content or order');
  const historyAccent = render('en', { card:{ kind:'cver', id:cardID('cver', { space:'space-a', id:'XC-orders', version:'1.5.0' }), accent:cardAccent('history-event', ['space-a','XC-orders','1.5.0','event-deprecate']) } });
  assert.equal(historyAccent.contractVersionCard.history[1].highlighted, true);
  const successorAccent = render('en', { card:{ kind:'cver', id:cardID('cver', { space:'space-a', id:'XC-orders', version:'1.5.0' }), accent:cardAccent('successor', ['space-a','XC-orders','1.5.0','XC-orders@2.2.0']) } });
  assert.match(successorAccent.contractVersionCard.successorStyle, /attention-tint/);
  successorAccent.contractVersionCard.openSuccessor();
  assert.deepEqual(opened.pop(), {
    kind:'cver',
    id:cardID('cver', { space:'space-a', id:'XC-orders', version:'2.2.0' }),
    accent:cardAccent('successor', ['space-a','XC-orders','1.5.0','XC-orders@2.2.0'])
  });

  const ru = render('ru', { card:{ kind:'cver', id:cardID('cver', { space:'space-a', id:'XC-orders', version:'1.5.0' }), accent:'' } });
  assert.equal(ru.contractVersionCard.documents[0].byteNotice, 'Показаны первые 64 байт из 380 байт.');
  assert.equal(ru.contractVersionCard.documents[1].byteNotice, '');
  assert.equal(ru.contractVersionCard.documentsWindowText, 'Показано 3 из 5.');
  assert.equal(ru.contractVersionCard.history[2].transitionResult.label, 'Вывод версии');
  assert.equal(ru.contractVersionCard.history[2].transition, 'Вывод версии');

  const screen = render('en', { space:'space-a', conSel:'XC-orders', conVersion:'1.5.0', card:null });
  assert.equal(screen.cdVersionHistory[2].transitionResult.label, 'Retired transition');
  assert.equal(screen.cdVersionHistory[2].actionText, 'Retired transition');
  assert.doesNotMatch(JSON.stringify(screen.cdVersionHistory), /<raw-retire-transition>/, 'raw transition enums must not leak through the screen timeline');
  assert.equal(screen.cdVersionNormativeTree[2].key, cardID('cfile', { space:'space-a', id:'XC-orders', version:'1.5.0', path:retiredDetail.documents[2].path }));
  assert.equal(screen.cdVersionNormativeTree[2].size, '0 B');
  const ruScreen = render('ru', { space:'space-a', conSel:'XC-orders', conVersion:'1.5.0', card:null });
  assert.equal(ruScreen.cdVersionHistory[2].transitionResult.label, 'Вывод версии');
  assert.equal(ruScreen.cdVersionHistory[2].actionText, 'Вывод версии');

  const empty = render('en', { card:{ kind:'contract', id:cardID('contract', emptyContract), accent:'' } });
  assert.equal(empty.contractCard.consumersEmpty, 'None recorded.');
  assert.equal(empty.contractCard.dependenciesEmpty, 'None recorded.');
  assert.equal(empty.contractCard.versionsEmpty, 'None recorded.');
  assert.equal(empty.contractCard.snapshotResult.label, 'source-freshness/unavailable');
  assert.equal(empty.contractCard.snapshotUnavailable, 'Unavailable in this snapshot: Committed operational evidence is unavailable.');

  const unknownOverview = render('en', { card:{ kind:'contract', id:cardID('contract', unknownContract), accent:'' } });
  assert.equal(unknownOverview.contractCard.lead, 'Unknown in this snapshot.');
  assert.doesNotMatch(unknownOverview.contractCard.lead, /XC-unknown|Unknown value|<raw-contract-state>/);
  const unknownVersion = render('en', { card:{ kind:'cver', id:cardID('cver', { space:'space-a', id:'XC-unknown', version:'9.9.9' }), accent:'' } });
  assert.equal(unknownVersion.contractVersionCard.lead, 'Unknown in this snapshot.');
  assert.doesNotMatch(unknownVersion.contractVersionCard.lead, /Version 9\.9\.9|Unknown value|<raw-version-state>/);

  const unavailableVersion = render('en', { card:{ kind:'cver', id:cardID('cver', { space:'space-b', id:'XC-unavailable', version:'0.9.0' }), accent:'' } });
  assert.equal(unavailableVersion.contractVersionCard.lead, 'Version 0.9.0 is lifecycle-state/published. Unavailable in this snapshot: Package history is unavailable.');
  assert.equal(unavailableVersion.contractVersionCard.unavailableText, 'Unavailable in this snapshot: Package history is unavailable.');
  assert.equal(unavailableVersion.contractVersionCard.snapshotResult.label, 'source-freshness/unavailable');
  assert.equal(unavailableVersion.contractVersionCard.snapshotUnavailable, 'Unavailable in this snapshot: Committed operational evidence is unavailable.');
  unavailableContract.versions[0].detail.unavailableReason = '';
  const unavailableWithoutReason = render('en', { card:{ kind:'cver', id:cardID('cver', { space:'space-b', id:'XC-unavailable', version:'0.9.0' }), accent:'' } });
  assert.equal(unavailableWithoutReason.contractVersionCard.unavailableText, 'Unknown in this snapshot.');
  data.operational.unavailable[1] = { source_kind:'space', space:'space-b', code:'space-read-unavailable', reason:'Technical source detail' };
  const sourceWithoutPublicReason = render('en', { card:{ kind:'contract', id:cardID('contract', emptyContract), accent:'' } });
  assert.equal(sourceWithoutPublicReason.contractCard.snapshotUnavailable, 'Unknown in this snapshot.');
  assert.equal(sourceWithoutPublicReason.contractCard.technical.at(-1).value, 'Technical source detail');

  data.aggregates.linesOffCurrent = { items:[], window:{ shown:0, total:5, truncated:true }, complete:false, degradations:[] };
  const emptyTruncatedAggregate = render('en', { space:'all', dashboardSet:'linesOffCurrent', card:null });
  assert.equal(emptyTruncatedAggregate.aggregateWindowText, 'Showing 0 of 5.');
  assert.equal(emptyTruncatedAggregate.aggregateEmptyText, '');
  data.aggregates.linesOffCurrent = { items:[], window:{ shown:0, total:0, truncated:true }, complete:true, degradations:[] };
  const fullyShownAggregate = render('en', { space:'all', dashboardSet:'linesOffCurrent', card:null });
  assert.equal(fullyShownAggregate.aggregateWindowText, '', 'an equal shown/total aggregate window must not claim truncation');
  data.aggregates.linesOffCurrent = { items:[], window:{ shown:0, total:5, truncated:false }, complete:true, degradations:[] };
  const nonTruncatedAggregate = render('en', { space:'all', dashboardSet:'linesOffCurrent', card:null });
  assert.equal(nonTruncatedAggregate.aggregateWindowText, '', 'a false truncation flag must suppress aggregate bounds even when shown is below total');
  data.aggregates.linesOffCurrent = { items:[], window:{ shown:0, total:0, truncated:false }, complete:true, degradations:[] };
  const completeEmptyAggregate = render('en', { space:'all', dashboardSet:'linesOffCurrent', card:null });
  assert.equal(completeEmptyAggregate.aggregateWindowText, '');
  assert.equal(completeEmptyAggregate.aggregateEmptyText, 'None recorded.');

  const compiledTemplate = runtimeDesignPage('ContractsView.dc.html').template;
  const factMarkers = (kind) => Array.from(compiledTemplate.matchAll(new RegExp(`data-card-fact="${kind}:([^"]+)"`, 'g')), match => match[1]);
  // Two regions per kind, not one flat list. P2 re-shaped the card contract
  // into a SUMMARY set and a DETAIL set, and in wave 3 P6 marked Contracts'
  // master-list row as the summary region — so `identity` and `condition`,
  // which card-spec places at `summary`, now legitimately appear twice in this
  // file: once in the card article and once on the row. This assertion
  // predated that model and read the file flat, so a correct change reddened
  // it. Split rather than widened: the frozen detail order is still asserted
  // exactly, and the summary set is asserted separately.
  //
  // The card articles precede the master list in this file, so the detail
  // markers come first.
  // 'operational' entered this order in 2770547b (P8, 2026-08-19): a contract
  // that promises an interface which does not exist yet now says so on the card,
  // between adoption and snapshot. The frozen list was not moved with it, so this
  // assertion has been red on main ever since — invisible because the npm lane is
  // not inside `make check` and CI has no Node. Moved deliberately, which is the
  // only way this list is ever allowed to move.
  const contractDetail = ['identity','condition','lead','description','consumers','dependencies','metadata','versions','adoption','operational','snapshot','technical'];
  const cverDetail = ['identity','condition','lead','publication','description-change','consumers','documents','metadata','history','successor','snapshot','technical'];
  const contractMarkers = factMarkers('contract');
  const cverMarkers = factMarkers('cver');
  assert.deepEqual(contractMarkers.slice(0, contractDetail.length), contractDetail, 'the frozen contract detail order must not move');
  assert.deepEqual(cverMarkers.slice(0, cverDetail.length), cverDetail, 'the frozen cver detail order must not move');
  assert.deepEqual(contractMarkers.slice(contractDetail.length), ['identity','condition'], "contract's summary region carries exactly its card-spec summary facts");
  assert.deepEqual(cverMarkers.slice(cverDetail.length), ['identity','condition'], "cver's summary region carries exactly its card-spec summary facts");

  const source = component('ContractsView');
  assert.doesNotMatch(source, /STATE_TONE|DRIFT_TONE|STATE_WORDS|DRIFT_WORDS|state\s*===\s*["']published["']/);
  assert.doesNotMatch(source, /\.sort\(|\.reverse\(/, 'contract/version/history/document presentation must retain carried order');
  assert.doesNotMatch(source, /conRiskCount|conRoleOf|conRisky|const\s+(?:risky|bad|seated)|ruPlural|driftWord/, 'browser-owned status membership and domain count helpers must be gone');
  assert.doesNotMatch(source, /contractEdges[\s\S]{0,160}(?:unique|new Set)|(?:unique|new Set)[\s\S]{0,160}contractEdges/, 'aggregate mode must not derive unique-contract counts');
});

test('EN and RU dashboard copy does not promise background autonomy', () => {
  assert.match(dashboardSource, /Bounded repository automation/);
  assert.match(dashboardSource, /no background watcher is implied/);
  assert.match(dashboardSource, /Ограниченная автоматизация через репозиторий/);
  assert.match(dashboardSource, /фоновый наблюдатель не подразумевается/);

  const forbidden = [
    /the full loop is automated/i,
    /exchanges continue while a person is busy or asleep/i,
    /everything else the agents finish on their own/i,
    /the agent handles them by protocol/i,
    /весь цикл автоматизирован/i,
    /обмен продолжается, пока человек занят или спит/i,
    /всё остальное агенты доводят сами/i,
    /агент разберётся с ними по протоколу/i,
  ];
  for (const claim of forbidden) assert.doesNotMatch(dashboardSource, claim);
});

test('public homepage keeps agent scheduling outside the a2a protocol claim', () => {
  assert.match(siteContent, /On each explicit agent run/);
  assert.match(siteContent, /continuous background execution remains the responsibility of each agent harness/);
  assert.match(publicHomeSource, /does not choose models, run agents or replace a repository's own delivery harness/);
  assert.match(publicHomeSource, /A2A_HOME_COPY:product-boundary:START/);

  const forbidden = [
    /continue the thread automatically/i,
    /never need a person to pass a message/i,
    /everything else the agents finish on their own/i,
    /exchanges continue while a person is busy or asleep/i,
  ];
  for (const claim of forbidden) {
    assert.doesNotMatch(siteContent, claim);
    assert.doesNotMatch(publicHomeSource, claim);
  }
});

test('EN and RU lifecycle copy does not infer work state from protocol state', () => {
  const forbidden = [
    /work complete · nobody owes a move/i,
    /document declined · work stopped/i,
    /current work stopped/i,
    /further work is not blocked/i,
    /blocks further work/i,
    /wait itself does not block further work/i,
    /work is stopped/i,
    /работа завершена · ход ни за кем/i,
    /документ отклонён · работа остановлена/i,
    /остановило текущую работу/i,
    /дальнейшая работа не заблокирована/i,
    /ожидание блокирует дальнейшую работу/i,
    /ожидание .* не блокирует дальнейшую работу/i,
    /работа стоит/i,
  ];
  for (const claim of forbidden) assert.doesNotMatch(dashboardSource, claim);
});
