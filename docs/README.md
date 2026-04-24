# docs/

Source for the [pincherMCP landing page](https://kwad77.github.io/pincherMCP/).

Single-file, zero-dependency static HTML. Edit `index.html`, push, GitHub
Pages redeploys in ~30 seconds.

## Enabling the site

One-time repo setting:

1. GitHub → Settings → Pages
2. Source: **Deploy from a branch**
3. Branch: **`master`** · Folder: **`/docs`**
4. Save

The `.nojekyll` sentinel file disables the Jekyll build step — GH Pages
serves `index.html` as-is without running any Ruby.

## Local preview

```bash
cd docs && python3 -m http.server 8000
# Open http://localhost:8000
```

## What NOT to put here

- API reference or generated docs — use the real README or a GH Pages
  subfolder if those become a thing. The landing page should stay a
  single file that loads in one paint.
- Analytics trackers or third-party JS — keep the zero-dependency
  promise that the dashboard also honours.
- Screenshots that aren't compressed. If you add an OG image, run it
  through `oxipng -o 4` or similar first.
