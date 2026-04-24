# docs/assets/ — art source spec

All art here is pixel-art, rendered via [PixelLab](https://www.pixellab.ai/)
or any equivalent tool (nanobanana/Gemini, Aseprite export, etc.). The
landing page's HTML already has `<img>` slots for every file listed
below — drop the PNG into this directory under the exact filename and
it just works.

**Character name:** *Pinchy*. A friendly pixel-art crab holding a shiny
copper penny in one claw. The pinch/crab/penny triad is the product
pitch: pincher "pinches pennies" on your LLM token bill, literally.

**CSS class on every pixel-art image:** `.pixel` — applies
`image-rendering: pixelated` so browsers don't smear the pixels when
they scale. Source images should be authored at the largest size that
will be used on screen, then let the browser downsample via CSS.

## Colour palette (match the site)

| Role | Hex | Use on Pinchy |
|---|---|---|
| Page background | `#0d1117` | Image background (matched, not transparent) |
| Surface / panels | `#161b22` | Shadow under Pinchy |
| Accent blue | `#58a6ff` | Eye highlight, stroke under the OG wordmark |
| Accent purple | `#a371f7` | Secondary highlight, complementary gradient stop |
| Green | `#3fb950` | Optional — a "$" sparkle next to the penny |
| Orange | `#f0883e` | Pinchy's shell base colour |
| Red | `#f85149` | Darker shade on Pinchy's claws + outline |
| Copper penny | `#b87333` | Penny base |
| Copper highlight | `#f4a460` | Penny inner shine |

Keep the active palette to **5–6 colours max** per sprite for that
classic pixel-art look. Don't use anti-aliasing; every edge is a clean
pixel boundary.

## Assets

### `pinchy.png` — main mascot (hero)

- **Size:** 256×256 source, displayed at 128×128 on the hero
- **Subject:** Pinchy facing the viewer, centred, front-on
- **Pose:** Holding a copper penny in the right claw (from viewer's POV)
  raised up beside the body like a "look what I got" gesture. Left claw
  hanging down relaxed.
- **Expression:** Tiny friendly eyes, the faintest curve of a smile.
  Not grinning — Pinchy is proud of the penny, not cartoonish.
- **Background:** Solid `#0d1117` so the image edges blend into the
  page with no seam. If transparent PNG is easier to produce, that's
  fine — the page background will show through.
- **Shadow:** Let the HTML `filter: drop-shadow` add the shadow; keep
  the sprite itself shadowless.

**PixelLab prompt suggestion:**
> A chunky pixel-art red-orange crab facing the viewer, holding a
> shiny copper penny raised in its right claw. Friendly black dot
> eyes, simple mouth, thick outline, 6-colour palette (red-orange
> body, darker red outline, copper penny, pale cream highlight, black
> eye, one blue accent pixel). No anti-aliasing. 256×256. Dark navy
> background.

### `pinchy-nav.png` — small Pinchy for the nav bar

- **Size:** 64×64 source, displayed at 28×28 in the nav
- **Subject:** Same Pinchy as the hero but in a simpler silhouette.
  The penny should still be visible — it's the brand hook — but the
  detail level drops to what reads at 28 px.
- **Pose:** Same front-facing, penny held up. You can lose the second
  claw entirely if it cleans up the silhouette.

If the penny doesn't read at 28 px, fall back to a ¾ profile Pinchy
where the penny is held forward toward the camera like a trophy.

### `crab.png` — section divider

- **Size:** 48×48 source, displayed at 20×20 inline with `<h2>`
- **Subject:** Just the crab. No penny. A tiny silhouette — think
  emoji-scale detail, not mascot-scale.
- **Pose:** Side profile, both claws visible, looking forward.
- Should read at 20 px so use **fat pixels** (probably 4×4 effective
  pixels at the source size).

**PixelLab prompt suggestion:**
> Tiny pixel-art crab, side profile, red-orange body, two claws,
> clean outline, 4-colour palette. 48×48. Transparent background.
> Emoji-scale simplicity.

### `favicon-32.png` — primary favicon

- **Size:** 32×32 exact
- **Subject:** Pinchy face + penny, cropped tight. The crab body and
  legs are mostly out of frame. The penny is prominent — about 8×8
  pixels so it reads unambiguously as a coin.
- Use 3–4 colours maximum.

### `favicon-16.png` — fallback favicon

- **Size:** 16×16 exact
- **Subject:** Simplest possible Pinchy silhouette. No penny — at this
  size a penny pixel-blob is indistinguishable from an eye. Just the
  crab-claw silhouette in one or two colours on a dark background.
- A 1-bit version (solid crab shape against contrasting background)
  is acceptable here.

### `apple-touch-icon.png` — iOS / Android home screen icon

- **Size:** 180×180 exact
- **Subject:** Same as `favicon-32.png` but at a bigger resolution
  with more of Pinchy's body visible. Can include the penny in full.
- Background should be a solid colour (iOS rounds the corners for
  you; transparency will be filled with black).

### `og.png` — social-preview image (Twitter/X, LinkedIn, Slack, Discord)

- **Size:** 1200×630 exact
- **Layout:** Pinchy on the left (roughly 300×300), text block on the
  right.
- **Text block:**
  - "**pincherMCP**" in big — 80–100 px type, white
  - "Codebase intelligence for LLM agents" below — 30–36 px type, muted
    grey (`#8b949e`)
  - Optional: the three keywords "*pure Go · local-only · no cloud*"
    in a smaller pill
- **Background:** Radial gradient from `#0d1117` in the corners to
  `#1a1f2e` toward the centre, same as the nav header. Add 3–5 tiny
  stray pennies scattered behind Pinchy for flavour.
- Use **non-pixel** typography for the text (crisp sans-serif) — only
  Pinchy and the pennies are pixel-art. Mixing pixel-art characters
  with clean type is the whole visual conceit.

**PixelLab prompt suggestion for the Pinchy figure on the OG image:**
> Same Pinchy as `pinchy.png` but slightly bigger and with a small
> cluster of 3 extra copper pennies orbiting him like coins tossed in
> the air. Dark navy background.

Text can be composited afterward in any image tool — or if PixelLab
supports text layers, add it there.

## Workflow when you generate the art

1. Generate each PNG at the source size listed above.
2. Save into this directory under the exact filename. (No renaming.)
3. Preview locally: `cd docs && python3 -m http.server 8000`, open
   `http://localhost:8000`.
4. Check that:
   - The nav Pinchy is sharp, not blurry (if blurry, the `.pixel`
     class isn't being applied — check the HTML)
   - The favicon shows in the browser tab
   - The OG image renders correctly by pasting the live URL into
     a [Twitter card validator](https://cards-dev.twitter.com/validator)
5. Commit the PNGs, push, GH Pages redeploys.

## Pinchy brand rules (informal)

- Pinchy is friendly, not menacing. No angry-face crab.
- Pinchy's penny is the punchline. Always visible at mascot scale.
- Pinchy is a **she**, a **he**, or neither — pronoun-neutral. The
  character has no gendering beyond "small crab holding a coin".
- Don't caption Pinchy with speech bubbles in the landing page. If
  we extend to a launch post or Twitter thread, single-line captions
  are fine ("Pinchy saves your agents from reading `utils.go` for
  the 500th time.").
