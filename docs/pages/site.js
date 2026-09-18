// Pages are plain JavaScript. Docs: https://veta.varavel.com/pages
// You can access environment, data, local files and remote sources
// using the destructured context.
export default function({ data, files, httpClient, parse }) {
  const home = parse.markdown("<note>A tiny site generated with **Veta**.</note>");
  const about = parse.markdown("This page was generated from `pages/site.js`.");

  return [
    {
      permalink: "/",
      template: "base",
      title: "Home",
      content: parse.renderComponents(home.html),
    },
    {
      permalink: "/about/",
      template: "base",
      title: "About",
      content: about.html,
    },
  ];
}
