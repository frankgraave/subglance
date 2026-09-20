#!/usr/bin/env python3
"""
Regenerate the self-hosted webfonts in web/public/fonts.

The product ships its two faces so the covered glyphs render without outbound
network access. They are checked into the repository as subsetted woff2 rather
than fetched from a CDN at build time or at page load. Glyphs outside the subset
use local system fonts; their appearance is not identical across machines.

Running this is a deliberate act, not part of `npm run build`: it needs network
access and a Python toolchain that the normal frontend build does not have. Run
it when a face is upgraded, then commit the result.

    python3 -m venv .venv
    .venv/bin/pip install -r scripts/font-requirements.txt
    .venv/bin/python scripts/subset-fonts.py

Everything that identifies a build lives in FACES below, so the generated files
can always be traced back to an exact upstream release.
"""

import argparse
import hashlib
import io
import json
import pathlib
import subprocess
import sys
import tempfile
import urllib.request
import zipfile

ROOT = pathlib.Path(__file__).resolve().parent.parent
OUT = ROOT / "public" / "fonts"

# Requested coverage, intersected with each upstream cmap. This is not a claim
# that either face contains every codepoint in these blocks. Other characters
# use the system fallback stack. See docs/font-coverage.md for the measured
# Greek/Cyrillic alternatives and the deliberate Latin boundary (SUB-109).
UNICODES = ",".join(
    [
        "U+0000-00FF",
        "U+0100-017F",
        "U+0192",
        "U+02BB-02BC",
        "U+02C6",
        "U+02DA",
        "U+02DC",
        "U+2000-206F",
        "U+20AC",
        "U+2122",
        "U+2190-2193",
        "U+2212",
        "U+FEFF",
        "U+FFFD",
    ]
)

# Layout features kept in the binary. Dropping a feature from the font is
# stronger than switching it off in CSS: the glyphs it needs go too, which is
# where most of the size saving comes from.
SANS_FEATURES = "calt,ccmp,locl,kern,mark,mkmk,rlig,cv01"
MONO_FEATURES = "calt,kern,mark,mkmk,ss01,ss02,ss03,ss04,ss05,cv01,cv05,cv06,cv11"

FACES = [
    {
        "name": "InterVariable",
        "version": "4.1",
        "source": "https://github.com/rsms/inter/releases/download/v4.1/Inter-4.1.zip",
        "sha256": "9883fdd4a49d4fb66bd8177ba6625ef9a64aa45899767dde3d36aa425756b11e",
        "member": "web/InterVariable.woff2",
        "licence": "LICENSE.txt",
        "licence_out": "InterVariable-OFL.txt",
        # The design system uses 400 through 600 only. Clipping the axis keeps
        # the variable face (so 450 is a real instance, not a snap to 400)
        # without paying for the eight weights nothing asks for.
        "instancer": ["wght=400:600"],
        "features": SANS_FEATURES,
        "out": "InterVariable-4.1-subset.woff2",
    },
    # Only the regular mono is shipped. Nothing that resolves to the mono role
    # asks for more than --weight-strong (500), so a bold file would be 20 kB
    # nobody downloads a glyph from; the @font-face range covers 400-500.
    {
        "name": "CommitMono 400",
        "version": "1.143",
        "source": "https://github.com/eigilnikolajsen/commit-mono/releases/download/v1.143/CommitMono-1.143.zip",
        "sha256": "f7d1f26a7c7554800a996f76f5d706bf0648b936ca2a66b5bc4d46e3a2c49ed0",
        "member": "CommitMono-1.143/CommitMono-400-Regular.otf",
        "licence": "CommitMono-1.143/license.txt",
        "licence_out": "CommitMono-OFL.txt",
        "instancer": [],
        "features": MONO_FEATURES,
        "out": "CommitMono-1.143-400-subset.woff2",
    },
]


def archive(url, want_sha):
    print(f"  fetching {url}")
    with urllib.request.urlopen(url) as response:
        blob = response.read()
    got = hashlib.sha256(blob).hexdigest()
    if got != want_sha:
        sys.exit(f"checksum mismatch for {url}\n  want {want_sha}\n  got  {got}")
    return zipfile.ZipFile(io.BytesIO(blob))


def main(out=OUT, unicodes=UNICODES, cache=None):
    out.mkdir(parents=True, exist_ok=True)
    manifest = []
    cache = {} if cache is None else cache
    for face in FACES:
        print(face["name"])
        zf = cache.get(face["source"])
        if zf is None:
            zf = cache[face["source"]] = archive(face["source"], face["sha256"])

        with tempfile.TemporaryDirectory() as tmp:
            tmp = pathlib.Path(tmp)
            src = tmp / pathlib.Path(face["member"]).name
            src.write_bytes(zf.read(face["member"]))

            if face["instancer"]:
                pinned = tmp / ("pinned" + src.suffix)
                subprocess.run(
                    [sys.executable, "-m", "fontTools.varLib.instancer",
                     str(src), *face["instancer"], "--no-recalc-timestamp", "-o", str(pinned)],
                    check=True, stdout=subprocess.DEVNULL,
                )
                src = pinned

            dst = out / face["out"]
            subprocess.run(
                [sys.executable, "-m", "fontTools.subset", str(src),
                 f"--unicodes={unicodes}",
                 f"--layout-features={face['features']}",
                 "--flavor=woff2",
                 "--no-hinting",
                 "--desubroutinize",
                 "--name-IDs=*",
                 f"--output-file={dst}"],
                check=True, stdout=subprocess.DEVNULL,
            )
            print(f"  {dst.name}: {dst.stat().st_size / 1024:.1f} kB")

        if face["licence"]:
            (out / face["licence_out"]).write_bytes(zf.read(face["licence"]))

        manifest.append(
            {
                "face": face["name"],
                "version": face["version"],
                "source": face["source"],
                "sha256": face["sha256"],
                "file": face["out"],
                "bytes": dst.stat().st_size,
                "subset_sha256": hashlib.sha256(dst.read_bytes()).hexdigest(),
                "unicodes": unicodes,
            }
        )

    (out / "MANIFEST.json").write_text(json.dumps(manifest, indent=2) + "\n")
    print(f"wrote {out / 'MANIFEST.json'}")
    return manifest


# Blocks, not language support promises. Include combining marks and extended
# forms so the comparison does not conceal the cost of accented names.
EXTRA_UNICODES = "U+0300-036F,U+0370-052F,U+1F00-1FFF,U+2DE0-2DFF,U+A640-A69F"
BLOCKS = {
    "combining": (0x0300, 0x036F),
    "greek": (0x0370, 0x03FF),
    "greek_extended": (0x1F00, 0x1FFF),
    "cyrillic": (0x0400, 0x052F),
    "cyrillic_extended_a": (0x2DE0, 0x2DFF),
    "cyrillic_extended_b": (0xA640, 0xA69F),
    "cjk_unified": (0x4E00, 0x9FFF),
}


def coverage(source):
    from fontTools.ttLib import TTFont
    with TTFont(source) as font:
        cmap = font.getBestCmap()
        return {block: sum(lo <= cp <= hi for cp in cmap)
                for block, (lo, hi) in BLOCKS.items()}


def measure(out):
    """Write candidates and a report to scratch, never to public/fonts."""
    import fontTools
    import brotli
    cache = {face["source"]: archive(face["source"], face["sha256"])
             for face in FACES}
    report = {
        "toolchain": {"fonttools": fontTools.__version__, "brotli": brotli.__version__},
        "blocks": {name: f"U+{lo:04X}-{hi:04X}" for name, (lo, hi) in BLOCKS.items()},
        "upstream": {face["name"]: coverage(io.BytesIO(
            cache[face["source"]].read(face["member"]))) for face in FACES},
        "profiles": {},
    }
    for name, unicodes in {
        "latin": UNICODES,
        "greek_cyrillic_basic": UNICODES + ",U+0300-036F,U+0370-052F",
        "greek_cyrillic": UNICODES + "," + EXTRA_UNICODES,
    }.items():
        manifest = main(out / name, unicodes, cache)
        report["profiles"][name] = {
            "total_bytes": sum(face["bytes"] for face in manifest),
            "faces": [{**face, "coverage": coverage(out / name / face["file"])}
                      for face in manifest],
        }
    (out / "comparison.json").write_text(json.dumps(report, indent=2) + "\n")
    print(f"wrote {out / 'comparison.json'}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--measure", type=pathlib.Path, metavar="SCRATCH_DIR",
                        help="compare Latin and Greek/Cyrillic subsets without changing shipped fonts")
    args = parser.parse_args()
    if args.measure:
        if args.measure.resolve().is_relative_to(OUT.resolve()):
            parser.error("measurement output must be outside public/fonts")
        measure(args.measure)
    else:
        main()
