# Self-hosted font coverage

The shipped boundary remains Latin-1, Latin Extended-A and selected punctuation
and symbols. InterVariable 4.1 sets the sans role; CommitMono 1.143 regular sets
the mono role. The exact requested codepoints and each binary's checksum are in
`web/public/fonts/MANIFEST.json`. Requested ranges are intersected with the
upstream font's character map, not promises that every character in a Unicode
block exists.

Greek and Cyrillic monitor names use the system fallback stack. A mixed name
such as `API Café Αθήνα Москва` uses the product face for Latin and system faces
for Greek/Cyrillic. This also applies to targets, tags and diagnostic text in
the mono role. Decomposed accents can use fallback for a whole combining
sequence; text is not normalised or rewritten to force a particular font.
Appearance and metrics outside the subset depend on the viewer's installed
fonts. CJK and other scripts need suitable local fonts; no extra font is fetched
from the internet, and a system without suitable glyphs may display missing
character boxes. No italic is shipped because the interface has no italic caller.

## Measurement and decision (SUB-109)

Measured with the archives and SHA-256 checks in `subset-fonts.py`, fonttools
4.60.0 and Brotli 1.1.0. All candidates use the same weight clipping, layout
features and hint removal as the shipped files. Sizes below are WOFF2 bytes
(over the wire, already compressed), not uncompressed source font sizes.

| Candidate | InterVariable bytes | CommitMono bytes | Total bytes |
|---|---:|---:|---:|
| Previously committed Latin | 48,260 | 21,076 | 69,336 |
| latin | 48,336 | 21,076 | 69,412 |
| greek_cyrillic_basic | 103,488 | 23,708 | 127,196 |
| greek_cyrillic | 111,188 | 23,708 | 134,896 |

`greek_cyrillic_basic` adds U+0300–036F and U+0370–052F. The full
`greek_cyrillic` candidate additionally requests Greek Extended and Cyrillic
Extended-A/B. Both preserve the existing Latin/symbol coverage. The regenerated
Latin binary differs by 76 bytes from the previously committed total; pinning
the toolchain and preserving the upstream timestamp makes regeneration
reproducible. The glyph boundary is unchanged.

The expanded subset is a real option independent of CJK, but exceeds the
81,920-byte font ceiling. It also leaves script support asymmetric: expanding
CommitMono cannot create the Cyrillic or polytonic Greek glyphs its upstream
does not contain. Retain the current boundary and fallback stacks rather than
increase every initial page load for incomplete additional coverage. The budget
stays unchanged. This decision does not mean that unsupported glyphs render in
the product face.

Upstream mapped codepoints (counts within blocks, not counts of languages):

| Block | InterVariable | CommitMono |
|---|---:|---:|
| Combining marks U+0300–036F | 66 | 5 |
| Greek U+0370–03FF | 105 | 72 |
| Greek Extended U+1F00–1FFF | 233 | 0 |
| Cyrillic U+0400–052F | 249 | 0 |
| Cyrillic Extended-A U+2DE0–2DFF | 1 | 0 |
| Cyrillic Extended-B U+A640–A69F | 1 | 0 |
| CJK Unified U+4E00–9FFF | 0 | 0 |

## Reproduce

From `web/`, using a scratch directory outside `public/fonts`:

```sh
python3 -m venv /tmp/subglance-fonts
/tmp/subglance-fonts/bin/pip install -r scripts/font-requirements.txt
/tmp/subglance-fonts/bin/python scripts/subset-fonts.py --measure /tmp/subglance-font-measurement
/tmp/subglance-fonts/bin/python scripts/test_font_coverage.py
```

Measurement downloads only the two checksum-verified upstream archives. It
writes candidate WOFF2 files, their manifests and `comparison.json` into the
scratch directory; it does not install candidates. Running without `--measure`
regenerates the shipped Latin subset and manifest. The toolchain is pinned and
the instancer preserves the upstream timestamp so repeated runs produce the
same bytes. Normal builds and tests do not download upstream fonts or require
Python; the binary coverage test above is an additional offline check.

`fonts.test.ts` binds the manifest's coverage decision to the shipped bytes.
`font-coverage.browser.test.ts` uses Chromium's reported fonts for painted
glyphs on the production monitor list and detail view. It tests Latin,
Greek/Cyrillic and mixed names, both font roles, keyboard navigation, name
contrast/accessibility and page overflow in light/dark at 375 and 1440 pixels.
These are layout fixtures, not a claim of changed API or storage behaviour.
The system font family itself is deliberately not pinned by a test.
