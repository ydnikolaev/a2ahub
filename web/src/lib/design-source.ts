import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const sourceRoot = resolve(process.cwd(), 'design-source');

// Exact-literal substitutions against the approved design source. A stale
// needle makes String.replace a silent no-op — the site builds green and the
// page behaves wrong — so every behavioural rewrite goes through `must`, which
// fails the build instead. Pure URL normalisation stays on plain replaceAll:
// it is idempotent and carries no behaviour.
function must(source: string, from: string, to: string) {
  if (!source.includes(from)) throw new Error(`design source: needle no longer present:\n${from}`);
  return source.replaceAll(from, to);
}
function mustAll(source: string, pairs: [string, string][]) {
  return pairs.reduce((acc, [from, to]) => must(acc, from, to), source);
}

function mustSlice(source: string, from: string, to: string) {
  const start = source.indexOf(from);
  const end = source.indexOf(to, start);
  if (start < 0 || end < 0) throw new Error(`design source: projection bounds no longer present:\n${from}\n${to}`);
  return source.slice(start, end);
}

const routes: Record<string, string> = {
  '13-public-home-v4.dc.html': '/',
  '14-local-dashboard-v4.dc.html': '/dashboard.html',
  '15-changelog-v4.dc.html': '/changelog.html',
  '16-docs-v4.dc.html': '/docs.html',
  '17-security-reliability-v4.dc.html': '/security.html',
  '18-install-v4.dc.html': '/install.html',
  '19-roadmap-v4.dc.html': '/roadmap.html',
  '20-dashboard-example-v4.dc.html': '/dashboard-example.html',
  '21-not-found-v4.dc.html': '/404.html'
};

function productionLinks(source: string) {
  let output = source;
  for (const [design, route] of Object.entries(routes)) output = output.replaceAll(design, route);
  return output
    .replaceAll('https://ydnikolaev.github.io/a2ahub/', 'https://a2ahub.dev/')
    .replaceAll('https://ydnikolaev.github.io/a2ahub', 'https://a2ahub.dev')
    .replaceAll('ydnikolaev.github.io/a2ahub', 'a2ahub.dev')
    .replaceAll('style-hover=', 'data-design-hover=');
}

function preserveDynamicTables(source: string) {
  return source.replace(/<(\/?)(table|thead|tbody|tfoot|tr|th|td)(\b[^>]*)>/gi, '<$1sc-raw-$2$3>');
}

export function runtimeDesignPage(file: string, variant?: 'guide') {
  const source = readFileSync(resolve(sourceRoot, file), 'utf8');
  const open = source.indexOf('<x-dc>');
  const close = source.lastIndexOf('</x-dc>');
  const scriptOpen = source.indexOf('<script type="text/x-dc" data-dc-script', close);
  const scriptBody = source.indexOf('>', scriptOpen) + 1;
  const scriptClose = source.indexOf('</script>', scriptBody);
  if (open < 0 || close < 0 || scriptOpen < 0 || scriptClose < 0) throw new Error(`approved design ${file} is incomplete`);

  let template = source.slice(open, close + '</x-dc>'.length)
    .replace(/<helmet>[\s\S]*?<\/helmet>/, '')
    .replace(/<div style="position:sticky; top:0; z-index:80;"><dc-import name="SiteHeader"[\s\S]*?<\/dc-import><\/div>/, '')
    .replace(/<dc-import name="SiteFooter"[\s\S]*?<\/dc-import>/, '');
  template = preserveDynamicTables(productionLinks(template));
  let logic = productionLinks(source.slice(scriptBody, scriptClose));
  if (file === '14-local-dashboard-v4.dc.html') {
    const componentTemplate = (name: string) => {
      const componentSource = readFileSync(resolve(sourceRoot, `${name}.dc.html`), 'utf8');
      const componentOpen = componentSource.indexOf('<x-dc>');
      const componentClose = componentSource.lastIndexOf('</x-dc>');
      if (componentOpen < 0 || componentClose < 0) throw new Error(`approved dashboard ${name} component is incomplete`);
      return preserveDynamicTables(productionLinks(
        componentSource.slice(componentOpen + '<x-dc>'.length, componentClose)
      ));
    };
    const componentLogic = (name: string) => {
      const componentSource = readFileSync(resolve(sourceRoot, `${name}.dc.html`), 'utf8');
      const componentClose = componentSource.lastIndexOf('</x-dc>');
      const componentScriptOpen = componentSource.indexOf('<script type="text/x-dc" data-dc-script', componentClose);
      const componentScriptBody = componentSource.indexOf('>', componentScriptOpen) + 1;
      const componentScriptClose = componentSource.indexOf('</script>', componentScriptBody);
      if (componentClose < 0 || componentScriptOpen < 0 || componentScriptClose < 0) throw new Error(`approved dashboard ${name} component logic is incomplete`);
      return productionLinks(componentSource.slice(componentScriptBody, componentScriptClose));
    };
    // The root literal imports are the projection graph. Keep each declaration
    // and its ctx binding together so markup expansion and logic composition
    // cannot drift into separate component registries.
    const rootContextImports = [...template.matchAll(/<dc-import name="([A-Z][A-Za-z0-9]*)" ctx="\{\{ ([A-Za-z][A-Za-z0-9]*) \}\}"[^>]*><\/dc-import>/g)]
      .map((match) => ({ declaration: match[0], name: match[1], ctx: match[2] }));
    const shellTemplate = componentTemplate('DashboardShell');
    const shellTopOpen = '<sc-if value="{{ isTop }}" hint-placeholder-val="{{ true }}">';
    const shellPageOpen = '<sc-if value="{{ isPage }}" hint-placeholder-val="{{ false }}">';
    const shellFooterOpen = '<sc-if value="{{ isFooter }}" hint-placeholder-val="{{ false }}">';
    const shellTopStart = shellTemplate.indexOf(shellTopOpen) + shellTopOpen.length;
    const shellPageMarker = `\n</sc-if>\n${shellPageOpen}`;
    const shellTopEnd = shellTemplate.indexOf(shellPageMarker, shellTopStart);
    const shellPageStart = shellTopEnd + shellPageMarker.length;
    const shellFooterMarker = `\n</sc-if>\n${shellFooterOpen}`;
    const shellPageEnd = shellTemplate.indexOf(shellFooterMarker, shellPageStart);
    const shellFooterStart = shellPageEnd + shellFooterMarker.length;
    const shellFooterEnd = shellTemplate.lastIndexOf('\n</sc-if>');
    if (shellTopStart < shellTopOpen.length || shellTopEnd < 0 || shellPageStart < shellPageMarker.length || shellPageEnd < shellPageStart || shellFooterStart < shellFooterMarker.length || shellFooterEnd < shellFooterStart) {
      throw new Error('approved dashboard shell component is incomplete');
    }
    template = must(
      template,
      '<dc-import name="DashboardShell" ctx="{{ shellTopCtx }}" hint-size="100%,84px"></dc-import>',
      shellTemplate.slice(shellTopStart, shellTopEnd),
    );
    template = must(
      template,
      '<dc-import name="DashboardShell" ctx="{{ shellPageCtx }}" hint-size="100%,160px"></dc-import>',
      shellTemplate.slice(shellPageStart, shellPageEnd),
    );
    template = must(
      template,
      '<dc-import name="DashboardShell" ctx="{{ shellFooterCtx }}" hint-size="100%,104px"></dc-import>',
      shellTemplate.slice(shellFooterStart, shellFooterEnd),
    );
    // Keep the source-facing projection useful to tests and tooling that audit
    // complete screens. The browser still composes literal dc-imports from the
    // root; this expansion is read-only and does not create a second runtime.
    const rootViewImports = rootContextImports.filter(({ name }) => name !== 'DashboardShell' && name !== 'Modal');
    if (!rootViewImports.length) throw new Error('approved dashboard root has no literal view imports');
    for (const { declaration, name } of rootViewImports) {
      template = must(
        template,
        declaration,
        componentTemplate(name),
      );
    }
    template = must(
      template,
      '<button type="button" onClick="{{ goBanner }}" style="flex:0 0 auto; font-family:\'Onest\',sans-serif; font-size:16px; font-weight:600; color:var(--teal-ink); background:transparent; border:0; cursor:pointer; white-space:nowrap;">{{ bannerAction }}</button>',
      '<a href="/" data-public-home-link style="flex:0 0 auto; font-family:\'Onest\',sans-serif; font-size:16px; font-weight:600; color:var(--teal-ink); white-space:nowrap; text-decoration:none;">{{ bannerAction }}</a>'
    );
    if (variant === 'guide') {
      // The public Features projection owns the same Guide markup as the
      // dashboard, now through the extracted component instead of a second
      // slice of the root template. The vocabulary and authored Guide data
      // remain in the root logic below, so the content generator keeps one
      // source for those constants.
      const guideTemplate = componentTemplate('GuideView');
      const guideScreen = '<section aria-label="{{ guideAria }}" data-screen-label="Guide">';
      if (!guideTemplate.includes(guideScreen)) throw new Error('approved dashboard Guide surface is incomplete');
      const guide = guideTemplate
        // The public page header already names this region with the same
        // visible title. Keep the shared section and screen marker, but avoid
        // exposing two identically named region landmarks to assistive tech.
        .replace(guideScreen, '<section data-screen-label="Guide">');
      template = `<x-dc><main style="width:min(1240px, 100% - 56px); margin:0 auto; padding:0 0 80px;">${guide}</main></x-dc>`;

      // The public Features route reuses the exact Guide template, but it
      // must not ship the dashboard's other eight screens and their complete
      // controller. Keep the shared Guide vocabulary/data declarations and a
      // purpose-built projection whose values match the dashboard render.
      const dashboardEN = mustSlice(logic, 'const DASHBOARD_EN = {', 'const DASHBOARD_RU = {');
      const glossaryEN = mustSlice(logic, 'const A2A_GLOSSARY_EN = {', 'const A2A_GLOSSARY_RU = {');
      const guideSupport = mustSlice(logic, 'const A2A_TYPE_GUIDE = ', 'const GUIDE_DOCS = ');
      const guideFeaturesEN = mustSlice(logic, 'const GUIDE_FEATURES_EN = [', 'const GUIDE_RU = {');
      logic = `${dashboardEN}
const DASHBOARD_I18N = { en: DASHBOARD_EN };
${glossaryEN}
const A2A_GLOSSARY = { en: A2A_GLOSSARY_EN };
${guideSupport}
${guideFeaturesEN}
class Component extends DCLogic {
  renderVals() {
    const c = DASHBOARD_I18N.en;
    return {
      isGuide: true,
      guideAria: c.guide,
      artifactTypesTitle: c.artifactTypesTitle,
      artifactTypesText: c.artifactTypesText,
      artifactTypes: A2A_TYPE_GUIDE.map((key) => ({ key, description: A2A_GLOSSARY.en.types[key].description })),
      feedbackTitle: c.feedbackTitle,
      feedbackText: c.feedbackText,
      feedbackPrompt: { text: "Use the a2ahub skill to file grounded feedback about a2ahub itself. Decide whether this is a bug, feature, docs, friction, or protocol report; inspect the current screen and reproduce or ground the request in real work; check a2a feedback status and the hub inbox/backlog for duplicates; create the draft with a2a feedback new <kind> --title <clear title>; complete its evidence and safety checks; run a2a feedback validate <file>; then submit it with a2a feedback submit <file>. Never include secrets or space-private data. If essential context is missing, ask me one focused question first." },
      githubRepoLabel: c.githubRepo,
      guideFeatures: GUIDE_FEATURES_EN.map(([tag, title, body]) => ({ tag, title, body })),
      lifecycleTitle: c.lifecycleTitle,
      lifecycleText: c.lifecycleText,
      lifecycleFoot: c.lifecycleFoot,
      statusGuide: A2A_STATUS_GUIDE.map((key) => ({
        key,
        description: A2A_GLOSSARY.en.statuses[key].description,
        tone: stateTone(key)
      })),
      readingGuideTitle: c.readingGuide,
      guideReadingRows: c.readingRows.map((row) => ({ label: row[0], text: row[1] }))
    };
  }
}
`;
    } else {
      // The site projection expands the extracted view markup so source-facing
      // tooling can inspect complete screens. Compose the corresponding
      // presentation classes into one projection controller as well; the
      // directly opened design source still uses the literal dc-import seams.
      const inlinedRootImports = rootContextImports.filter(({ declaration }) => !template.includes(declaration));
      const inlinedComponentNames = [...new Set(inlinedRootImports.map(({ name }) => name))];
      const projectionClasses = inlinedComponentNames.map((name) => {
        let childLogic = componentLogic(name);
        if (name === 'DashboardShell') {
          childLogic = must(
            childLogic,
            'bannerAction: d.meta.synthetic ? (ru ? "Про демо →" : "About the demo →") : (ru ? "Версии →" : "Versions →"),',
            'bannerAction: d.meta.synthetic ? (ru ? "На главную →" : "Back to home →") : (ru ? "Версии →" : "Versions →"),'
          );
        }
        return `const ${name}Projection = (() => {\n${childLogic}\nreturn Component;\n})();`;
      }).join('\n');
      const projectionRenders = inlinedRootImports
        .map(({ name, ctx }) => `    Object.assign(values, renderPart(${name}Projection, values.${ctx}));`)
        .join('\n');
      logic = logic.replace('class Component extends DCLogic {', 'class DashboardRootComponent extends DCLogic {');
      logic += `\n${projectionClasses}\nclass Component extends DashboardRootComponent {\n  renderVals() {\n    const values = super.renderVals();\n    const renderPart = (Part, ctx) => { const part = new Part(); part.props = { ctx }; return part.renderVals(); };\n${projectionRenders}\n    return values;\n  }\n}\n`;
    }
  }
  if (file === '15-changelog-v4.dc.html') logic = must(logic, 'const INDEX = [', 'const INDEX = window.A2A_RELEASE_INDEX || [');
  if (file === '16-docs-v4.dc.html') {
    template = mustAll(template, [
      ['<button type="button" onClick="{{ it.go }}" style="{{ it.style }}">{{ it.label }}</button>', '<a href="{{ it.href }}" style="{{ it.style }}">{{ it.label }}</a>'],
      ['<sc-if value="{{ isPlaceholder }}" hint-placeholder-val="{{ false }}">', '<sc-if value="{{ hasDocBody }}" hint-placeholder-val="{{ true }}">'],
      ['<sc-if value="{{ isThreads }}" hint-placeholder-val="{{ true }}">', '<sc-if value="{{ renderCustomDocs }}" hint-placeholder-val="{{ false }}">'],
      ['<sc-if value="{{ isCommands }}" hint-placeholder-val="{{ false }}">', '<sc-if value="{{ renderCustomDocs }}" hint-placeholder-val="{{ false }}">'],
      ['<div style="background:var(--page); border-radius:var(--radius-card); padding:var(--padding-card-block) var(--padding-card-inline); box-shadow:inset 0 0 0 1px var(--border);">', '<div data-doc-placeholder data-doc-id="{{ docId }}" style="background:var(--page); border-radius:var(--radius-card); padding:var(--padding-card-block) var(--padding-card-inline); box-shadow:inset 0 0 0 1px var(--border);">'],
      ['<div style="font-size:15px; font-weight:600; margin-bottom:10px;">On this page</div>', '<div data-doc-toc><div style="font-size:15px; font-weight:600; margin-bottom:10px;">On this page</div>'],
      [
        '<sc-if value="{{ noToc }}" hint-placeholder-val="{{ false }}">\n            <div style="font-size:15px; line-height:1.55; color:var(--muted);">Headings come from the injected document, so this list is built at the same time as the body.</div>\n          </sc-if>\n        </div>',
        '<sc-if value="{{ noToc }}" hint-placeholder-val="{{ false }}">\n            <div style="font-size:15px; line-height:1.55; color:var(--muted);">Headings come from the injected document, so this list is built at the same time as the body.</div>\n          </sc-if>\n          </div>\n        </div>'
      ]
    ]);
    logic = mustAll(logic, [
      ['const DOCS = [', 'const DOCS = window.A2A_DOCS || ['],
      ['state = { doc: "threads", q: "", copied: "" };', 'state = { doc: ((location.pathname.match(/\\/docs\\/([^/.]+)/) || [])[1] || "threads"), q: "", copied: "" };'],
      ['label, style: this.itemStyle(this.state.doc === id),', 'label, href: "/docs/" + id + ".html", style: this.itemStyle(this.state.doc === id) + " text-decoration:none;",'],
      ['docTitle: cur.title,', 'docId: cur.id,\n      docTitle: cur.title,'],
      ['isThreads, isCommands, isPlaceholder,', 'isThreads, isCommands, isPlaceholder, hasDocBody: true, renderCustomDocs: false,'],
      [
        'docLead: LEAD[cur.id] || "This route is one document in the canonical corpus embedded in the binary. The prototype shows its shell, its actions and its metadata without inventing its text.",',
        'docLead: LEAD[cur.id] || "This route is one document in the canonical corpus embedded in the binary. Its body, search text and Markdown twin are projected from that same source.",'
      ]
    ]);
  }
  logic = logic.replaceAll('/a2ahub/', '/');
  return { template, logic };
}
