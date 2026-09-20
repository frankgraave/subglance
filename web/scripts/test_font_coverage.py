"""Hermetic binary coverage checks; run with the pinned font toolchain."""
import importlib.util
import unittest
from pathlib import Path
from fontTools.ttLib import TTFont

spec = importlib.util.spec_from_file_location("subset", Path(__file__).with_name("subset-fonts.py"))
subset = importlib.util.module_from_spec(spec)
spec.loader.exec_module(subset)


class CoverageTest(unittest.TestCase):
    def test_shipped_boundary(self):
        for face in subset.FACES:
            with self.subTest(face=face["name"]):
                with TTFont(subset.OUT / face["out"]) as font:
                    cmap = font.getBestCmap()
                    for char in "Café Łódź € ← −":
                        self.assertIn(ord(char), cmap)
                        self.assertGreater(font.getGlyphID(cmap[ord(char)]), 0)
                    for char in "ΑθήναМоскваἈ東京":
                        self.assertNotIn(ord(char), cmap)
                self.assertEqual(set(subset.coverage(subset.OUT / face["out"]).values()), {0})


if __name__ == "__main__":
    unittest.main()
