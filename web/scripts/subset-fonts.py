#!/usr/bin/env python3
"""
Regenerate the self-hosted webfonts in web/public/fonts.

The product is self-hosted and has to render identically on a machine with no
outbound network, so the two faces the design system is built on are checked
into the repository as subsetted woff2 rather than fetched from a CDN at build
time or at page load.

Running this is a deliberate act, not part of `npm run build`: it needs network
access and a Python toolchain that the normal frontend build does not have. Run
it when a face is upgraded, then commit the result.

    python3 -m venv .venv && .venv/bin/pip install fonttools brotli
    .venv/bin/python scripts/subset-fonts.py

Everything that identifies a build lives in FACES below, so the generated files
can always be traced back to an exact upstream release.
"""

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

# The characters the interface can render. Latin-1 plus Latin Extended-A covers
# European monitor names and the ASCII the UI is written in; the general
# punctuation block carries the em dash, the arrows block the back arrow, and
# the odd currency and typographic character is there because user-entered text
# reaches for it. Anything outside this falls back to a system face, which is
# the correct trade for a dashboard: the alternative is shipping CJK.
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


def main():
    OUT.mkdir(parents=True, exist_ok=True)
    manifest = []
    cache = {}
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
                     str(src), *face["instancer"], "-o", str(pinned)],
                    check=True, stdout=subprocess.DEVNULL,
                )
                src = pinned

            dst = OUT / face["out"]
            subprocess.run(
                [sys.executable, "-m", "fontTools.subset", str(src),
                 f"--unicodes={UNICODES}",
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
            (OUT / face["licence_out"]).write_bytes(zf.read(face["licence"]))

        manifest.append(
            {
                "face": face["name"],
                "version": face["version"],
                "source": face["source"],
                "sha256": face["sha256"],
                "file": face["out"],
                "bytes": (OUT / face["out"]).stat().st_size,
            }
        )

    (OUT / "MANIFEST.json").write_text(json.dumps(manifest, indent=2) + "\n")
    print(f"wrote {OUT / 'MANIFEST.json'}")


if __name__ == "__main__":
    main()
