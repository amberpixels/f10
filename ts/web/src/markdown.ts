import MarkdownIt from "markdown-it";

// Raw HTML stays off, so a plan or a finding cannot put markup in the page.
const md = new MarkdownIt({ html: false, linkify: true, breaks: false });

const defaultLink =
  md.renderer.rules.link_open ?? ((tokens, i, opts, _env, self) => self.renderToken(tokens, i, opts));

md.renderer.rules.link_open = (tokens, i, opts, env, self) => {
  tokens[i].attrSet("target", "_blank");
  tokens[i].attrSet("rel", "noopener noreferrer");
  return defaultLink(tokens, i, opts, env, self);
};

export function renderMarkdown(src: string): string {
  return md.render(src);
}
