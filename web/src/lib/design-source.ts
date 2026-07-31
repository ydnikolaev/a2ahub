import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const sourceRoot = resolve(process.cwd(), 'design-source');

const routes: Record<string, string> = {
  '12-design-system-v4.dc.html': '/design-system.html',
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

function copyButton(source: string, key: string) {
  const command = 'curl -fsSL https://raw.githubusercontent.com/ydnikolaev/a2ahub/main/scripts/install.sh | sh';
  return source
    .replace(`onClick="{{ ${key}.copy }}"`, `data-copy-value="${command}"`)
    .replace(`style="{{ ${key}.style }}"`, `style="margin-left:auto;font-family:'Onest',sans-serif;font-size:14px;font-weight:600;border:0;border-radius:8px;padding:5px 11px;cursor:pointer;white-space:nowrap;background:var(--inverse-3);color:var(--teal-on-dark);"`)
    .replace(`{{ ${key}.label }}`, 'Copy');
}

export function publicHomeDesign() {
  const source = readFileSync(resolve(sourceRoot, '13-public-home-v4.dc.html'), 'utf8');
  const start = source.indexOf('<main>');
  const end = source.indexOf('</main>', start);
  if (start < 0 || end < 0) throw new Error('approved home design has no main element');
  let main = source.slice(start + '<main>'.length, end);
  main = copyButton(copyButton(productionLinks(main), 'install'), 'install2');

  // The standalone delivery is the rendered authority: it contains this title
  // once. One prototype source revision accidentally duplicated the adjacent h2.
  const title = '<h2 style="margin:0 0 12px; font-size:42px; line-height:1.1; font-weight:600; letter-spacing:-0.03em; max-width:760px; text-wrap:balance;">Agents run the protocol. Humans get the explanation</h2>';
  main = main.replace(`${title}\n          ${title}`, title);

  const map = /<dc-import name="NetworkMap"[^>]*><\/dc-import>/;
  const match = main.match(map);
  if (!match || match.index === undefined) throw new Error('approved home design has no NetworkMap mount');
  return {
    beforeMap: main.slice(0, match.index),
    afterMap: main.slice(match.index + match[0].length)
  };
}

export function runtimeDesignPage(file: string) {
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
  if (file === '15-changelog-v4.dc.html') logic = logic.replace('const INDEX = [', 'const INDEX = window.A2A_RELEASE_INDEX || [');
  if (file === '16-docs-v4.dc.html') {
    template = template
      .replace('<div style="background:var(--page); border-radius:14px; padding:20px 22px; box-shadow:inset 0 0 0 1px var(--border);">', '<div data-doc-placeholder data-doc-id="{{ docId }}" style="background:var(--page); border-radius:14px; padding:20px 22px; box-shadow:inset 0 0 0 1px var(--border);">')
      .replace('<div style="font-size:15px; font-weight:600; margin-bottom:10px;">On this page</div>', '<div data-doc-toc><div style="font-size:15px; font-weight:600; margin-bottom:10px;">On this page</div>');
    template = template.replace(
      '<sc-if value="{{ noToc }}" hint-placeholder-val="{{ false }}">\n            <div style="font-size:15px; line-height:1.55; color:var(--muted);">Headings come from the injected document, so this list is built at the same time as the body.</div>\n          </sc-if>\n        </div>',
      '<sc-if value="{{ noToc }}" hint-placeholder-val="{{ false }}">\n            <div style="font-size:15px; line-height:1.55; color:var(--muted);">Headings come from the injected document, so this list is built at the same time as the body.</div>\n          </sc-if>\n          </div>\n        </div>'
    );
    logic = logic
      .replace('const DOCS = [', 'const DOCS = window.A2A_DOCS || [')
      .replace('state = { doc: "threads", q: "", copied: "" };', 'state = { doc: ((location.pathname.match(/\\/docs\\/([^/.]+)/) || [])[1] || "threads"), q: "", copied: "" };')
      .replace('go: () => this.setState({ doc: id })', 'go: () => { history.pushState({}, "", "/docs/" + id + ".html"); this.setState({ doc: id }); }')
      .replace('docTitle: cur.title,', 'docId: cur.id,\n      docTitle: cur.title,')
      .replace(
        'docGenerated: isCommands ? "regenerated with every release · last: v0.16.3, 2026-07-30" : "projected at build time from v0.16.3",',
        'docGenerated: isCommands ? "regenerated with every release · last: v" + window.A2A_LATEST_RELEASE.version + ", " + window.A2A_LATEST_RELEASE.released : "projected at build time from v" + window.A2A_LATEST_RELEASE.version,'
      )
      .replace(
        'docLead: LEAD[cur.id] || "This route is one document in the canonical corpus embedded in the binary. The prototype shows its shell, its actions and its metadata without inventing its text.",',
        'docLead: LEAD[cur.id] || "This route is one document in the canonical corpus embedded in the binary. Its body, search text and Markdown twin are projected from that same source.",'
      );
  }
  logic = logic.replaceAll('/a2ahub/', '/');
  return { template, logic };
}
