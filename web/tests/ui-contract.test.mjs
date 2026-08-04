import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';
import { runtimeDesignPage } from '../src/lib/design-source.ts';

const repoFile = path => readFileSync(new URL(`../../${path}`, import.meta.url), 'utf8');
const tokens = repoFile('ui/tokens.css');
const dashboardSource = repoFile('web/design-source/14-local-dashboard-v4.dc.html');
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

function dashboardController() {
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
    location: { protocol: 'http:', hostname: '127.0.0.1', search: '', hash: '' },
    localStorage: { getItem() { return null; }, setItem() {} },
    navigator: { clipboard: { writeText: async () => {} } },
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

test('shared card, panel and modal components consume semantic hierarchy tokens', () => {
  const timeline = component('TimelineArtifactCard');
  assert.match(timeline, /var\(--font-card-title-md\)/);
  assert.match(timeline, /var\(--line-card-title-md\)/);
  assert.match(timeline, /var\(--radius-nested\)/);
  assert.match(timeline, /var\(--padding-nested-block\)/);

  const detail = component('LinkDetail');
  assert.match(detail, /class="a2a-detail-title"/);
  assert.match(detail, /var\(--radius-panel\)/);
  assert.match(detail, /var\(--padding-panel-block\)/);
  assert.match(detail, /width:38px; height:38px/);
  assert.match(detail, /aria-label="\{\{ closeLabel \}\}"/);

  const map = component('NetworkMap');
  assert.match(map, /border-radius:var\(--radius-panel\)/);
  assert.match(map, /border-radius:var\(--radius-card\); padding:var\(--padding-list-card-block\)/);
  assert.match(map, /padding:var\(--padding-modal-backdrop\)/);
});

test('inactive master-list records use the shared contrast-safe container cue', () => {
  const row = component('ArtifactRow');
  assert.match(row, /var\(--surface-inactive-card\)/);
  assert.match(row, /var\(--shadow-inactive-card\)/);
  assert.doesNotMatch(row, /opacity-inactive-card/);
  assert.doesNotMatch(row, /opacity\s*:/);
});

test('Exchange orders committed work reports by commit sequence with an artifact-id tie break', () => {
  const controller = dashboardController();
  const data = exchangeSubjectFixture();
  controller.state.data = data;

  const detail = controller.detailFor('XW-target', data.inbox);
  assert.deepEqual(
    Array.from(detail.events.filter(event => event.isWorkReport), event => event.workSummary),
    ['tie b', 'tie z', 'older commit but newer authored time', 'artifact subject'],
  );
  assert.match(detail.events.find(event => event.workSummary === 'older commit but newer authored time').meta, /12:00/);
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
    [false, 'added'],
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
  values.threadRows[1].select();
  assert.equal(controller.state.threadSel, first.id);
  assert.equal(controller.state.threadSpace, secondSpace);

  values = controller.renderVals();
  assert.equal(values.tvSpaceText, secondSpace);
  assert.equal(values.tvTitle, 'Duplicate thread in second space');
  assert.deepEqual(Array.from(values.threadRows, row => row.selected), [false, true]);
});

test('Guide keeps the new operational and exact-contract cards in EN/RU parity', () => {
  const controller = dashboardController();
  controller.state.locale = 'en';
  const english = controller.renderVals().guideFeatures;
  controller.state.locale = 'ru';
  const russian = controller.renderVals().guideFeatures;

  assert.equal(english.length, russian.length);
  assert.deepEqual(Array.from(english.slice(-2), card => card.tag), ['work report', 'id@version']);
  assert.deepEqual(Array.from(russian.slice(-2), card => card.tag), ['work report', 'id@version']);
  assert.match(english.at(-2).body, /unknown, never idle/);
  assert.match(russian.at(-2).body, /«неизвестно».*«простой»/);
  assert.match(english.at(-1).body, /immutable carried set.*preflight, materialize, and check/);
  assert.match(russian.at(-1).body, /неизменяемый набор файлов.*preflight, materialize и check/);
});

test('bounded contract-version packages disclose omitted files in EN/RU', () => {
  const controller = dashboardController();
  const data = JSON.parse(repoFile('internal/html/testdata/demo.json'));
  const contract = data.contracts.find(candidate =>
    (candidate.versions || []).some(version => version.detail?.status === 'available'));
  const version = contract.versions.find(candidate => candidate.detail?.status === 'available');
  const shown = version.detail.documents.length;
  version.detail.totalDocumentCount = shown + 7;
  version.detail.omittedDocumentCount = 7;
  controller.state.data = data;
  controller.state.view = 'contracts';
  controller.state.space = contract.space;
  controller.state.conSel = contract.id;
  controller.state.conVersion = version.version;

  controller.state.locale = 'en';
  let values = controller.renderVals();
  assert.equal(values.cdVersionDocumentsOmitted, true);
  assert.match(values.cdVersionDocumentsOmittedText, new RegExp(`includes ${shown} of ${shown + 7} files`));
  assert.match(values.cdVersionDocumentsOmittedText, /7 more are not embedded because of the dashboard-wide bound/);
  assert.match(values.cdVersionDocumentsOmittedText, /Shown digests and metadata apply only to the listed files/);

  controller.state.locale = 'ru';
  values = controller.renderVals();
  assert.equal(values.cdVersionDocumentsOmitted, true);
  assert.match(values.cdVersionDocumentsOmittedText, new RegExp(`В снимок вошло файлов: ${shown} из ${shown + 7}`));
  assert.match(values.cdVersionDocumentsOmittedText, /ещё 7 не встроено из-за общего лимита дашборда/);
  assert.match(values.cdVersionDocumentsOmittedText, /digest и метаданные точны только для перечисленных файлов/);

  version.detail.totalDocumentCount = shown;
  version.detail.omittedDocumentCount = 0;
  assert.equal(controller.renderVals().cdVersionDocumentsOmitted, false);
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
