# Termivoa website

The landing page and the docs at <https://termivoa.com>.

- `public/` is the landing page: plain HTML and CSS, served as-is at `/`.
- `src/content/docs/docs/` are the docs pages (Markdown), served at `/docs/` by
  [Astro Starlight](https://starlight.astro.build). The sidebar order is in `astro.config.mjs`.
- `src/styles/docs.css` gives the docs the same colours and fonts as the landing page.

## Work on it

```sh
cd site
pnpm install --ignore-workspace
pnpm dev        # http://localhost:4321, live reload
pnpm build      # static output in dist/
```

Deploy: `cd site && vercel deploy --prod` (the folder is linked to the Vercel project `termivoa`; Vercel runs the build).

## Add a docs page

1. Create `src/content/docs/docs/<section>/<name>.md` with `title` and `description` frontmatter.
2. Add `'docs/<section>/<name>'` to the sidebar in `astro.config.mjs`.
3. Write short sentences in simple English. One task per page. Commands in code blocks.

## Notes

- Phone screens in `public/assets/` are exported from the Paper file `termivoa` (page "Phone app v1").
  Assets are cached for a week, so give a changed image a new file name.
- `public/_og.html` renders `public/assets/og.png` (1200×630) with headless Chrome; it is not deployed.
- Fonts are self-hosted under the SIL Open Font License 1.1: Newsreader, Caveat and JetBrains Mono.
  See `public/fonts/OFL-*.txt`.
