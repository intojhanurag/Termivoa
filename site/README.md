# Termivoa website

The static landing page at <https://termivoa.vercel.app>. Plain HTML and CSS, no build step.

- Preview: `python3 -m http.server 4321 -d site`, then open <http://127.0.0.1:4321>.
- Deploy: `cd site && vercel deploy --prod` (the folder is linked to the Vercel project `termivoa`).
- Phone screens in `assets/` are exported from the Paper file `termivoa` (page "Phone app v1"). Assets are cached for a week, so give a changed image a new file name.
- `_og.html` renders `assets/og.png` (1200×630) with headless Chrome; it is not deployed.

Fonts are self-hosted under the SIL Open Font License 1.1: Newsreader, Caveat and JetBrains Mono. See `fonts/OFL-*.txt`.
