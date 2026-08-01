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
    template = must(
      template,
      '<button type="button" onClick="{{ goBanner }}" style="flex:0 0 auto; font-family:\'Onest\',sans-serif; font-size:16px; font-weight:600; color:var(--teal-ink); background:transparent; border:0; cursor:pointer; white-space:nowrap;">{{ bannerAction }}</button>',
      '<a href="/" data-public-home-link style="flex:0 0 auto; font-family:\'Onest\',sans-serif; font-size:16px; font-weight:600; color:var(--teal-ink); white-space:nowrap; text-decoration:none;">{{ bannerAction }}</a>'
    );
    logic = must(
      logic,
      'bannerAction: d.meta.synthetic ? (ru ? "Про демо →" : "About the demo →") : (ru ? "Версии →" : "Versions →"),',
      'bannerAction: d.meta.synthetic ? (ru ? "На главную →" : "Back to home →") : (ru ? "Версии →" : "Versions →"),'
    );
    if (variant === 'guide') {
      const guideOpen = '<sc-if value="{{ isGuide }}" hint-placeholder-val="{{ false }}">';
      // `isGuide` also controls the compass in the dashboard navigation. The
      // public Features page needs the Guide screen itself, not every screen
      // between that icon and the Guide's closing tag. Anchor the extraction
      // on the screen's unique marker, then walk back to its owning condition.
      const guideScreen = '<section aria-label="{{ guideAria }}" data-screen-label="Guide">';
      const guideScreenStart = template.indexOf(guideScreen);
      const guideStart = template.lastIndexOf(guideOpen, guideScreenStart);
      const guideEndMarker = '\n    </sc-if>\n\n  </main>';
      const guideEnd = template.indexOf(guideEndMarker, guideStart);
      if (guideScreenStart < 0 || guideStart < 0 || guideEnd < 0) throw new Error('approved dashboard Guide surface is incomplete');
      const guide = template
        .slice(guideStart, guideEnd + '\n    </sc-if>'.length)
        // The public page header already names this region with the same
        // visible title. Keep the shared section and screen marker, but avoid
        // exposing two identically named region landmarks to assistive tech.
        .replace(guideScreen, '<section data-screen-label="Guide">');
      template = `<x-dc><main style="width:min(1240px, 100% - 56px); margin:0 auto; padding:0 0 80px;">${guide}</main></x-dc>`;

      // The public Features route reuses the exact Guide template, but it
      // must not ship the dashboard's other eight screens and their complete
      // controller. Keep the shared Guide vocabulary/data declarations and a
      // purpose-built projection whose values match the dashboard render.
      const sharedStart = logic.indexOf('const DASHBOARD_I18N = {');
      const sharedEnd = logic.indexOf('const VIEW_IDS = ', sharedStart);
      if (sharedStart < 0 || sharedEnd < 0) throw new Error('approved dashboard Guide logic is incomplete');
      logic = `${logic.slice(sharedStart, sharedEnd)}
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
        tone: key === "blocking" ? "blocking" : (["blocked", "your approval pending", "overdue", "disputed", "cancelled", "withdrawn", "space stale"].includes(key) ? "attention" : (["verified", "closed"].includes(key) ? "healthy" : "neutral"))
      })),
      readingGuideTitle: c.readingGuide,
      readOnlyLabel: c.readOnly,
      guideReadingRows: c.readingRows.map((row, index) => ({
        label: row[0],
        text: row[1],
        labelStyle: "padding:13px 20px 13px 0; width:210px; vertical-align:top; font-weight:600;" + (index < c.readingRows.length - 1 ? " border-bottom:1px solid var(--hairline);" : ""),
        textStyle: "padding:13px 0; line-height:1.6; color:var(--body);" + (index < c.readingRows.length - 1 ? " border-bottom:1px solid var(--hairline);" : "")
      }))
    };
  }
}
`;
    }
  }
  if (file === '15-changelog-v4.dc.html') logic = must(logic, 'const INDEX = [', 'const INDEX = window.A2A_RELEASE_INDEX || [');
  if (file === '16-docs-v4.dc.html') {
    template = mustAll(template, [
      ['<button type="button" onClick="{{ it.go }}" style="{{ it.style }}">{{ it.label }}</button>', '<a href="{{ it.href }}" style="{{ it.style }}">{{ it.label }}</a>'],
      ['<sc-if value="{{ isPlaceholder }}" hint-placeholder-val="{{ false }}">', '<sc-if value="{{ hasDocBody }}" hint-placeholder-val="{{ true }}">'],
      ['<sc-if value="{{ isThreads }}" hint-placeholder-val="{{ true }}">', '<sc-if value="{{ renderCustomDocs }}" hint-placeholder-val="{{ false }}">'],
      ['<sc-if value="{{ isCommands }}" hint-placeholder-val="{{ false }}">', '<sc-if value="{{ renderCustomDocs }}" hint-placeholder-val="{{ false }}">'],
      ['<div style="background:var(--page); border-radius:14px; padding:20px 22px; box-shadow:inset 0 0 0 1px var(--border);">', '<div data-doc-placeholder data-doc-id="{{ docId }}" style="background:var(--page); border-radius:14px; padding:20px 22px; box-shadow:inset 0 0 0 1px var(--border);">'],
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
