"""Tests for notices.py's file notices: python3 -m unittest discover -s scripts -p '*_test.py'"""
from pathlib import Path
import tempfile
import unittest

from notices import file_notices, notice_comment

GO_LICENCE = "Copyright (c) 2009 The Go Authors. All rights reserved. Redistribution and use in source and binary forms..."


class FileNotices(unittest.TestCase):
    def write(self, name: str, text: str) -> Path:
        p = Path(self.dir.name, name)
        p.write_text(text)
        return p

    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()

    def tearDown(self):
        self.dir.cleanup()

    def test_a_notice_after_the_package_clause(self):
        p = self.write("bitcurve.go", "package bitcurves\n\n// Copyright 2010 The Go Authors. All rights reserved.\n// Copyright 2011 ThePiachu. All rights reserved.\n\n// Package bitelliptic ...\npackage x\n")
        self.assertIn("ThePiachu", notice_comment(p))
        self.assertEqual([n for n, _ in file_notices([p], Path(self.dir.name), GO_LICENCE)], ["bitcurve.go (the file's own notice)"])

    def test_an_unstarred_block_comment_after_build_lines(self):
        p = self.write("os_bound.go", "//go:build !js\n// +build !js\n\n/*\n   Copyright 2022 The Flux authors.\n\n   Licensed under the Apache License.\n*/\n\npackage osfs\n")
        self.assertEqual(notice_comment(p), "Copyright 2022 The Flux authors.\n\nLicensed under the Apache License.")

    def test_a_starred_block_and_assembly_comments(self):
        p = self.write("a.go", "/*\n * Copyright 2014 Matthew Endsley\n * All rights reserved\n */\npackage keywrap\n")
        self.assertEqual(notice_comment(p), "Copyright 2014 Matthew Endsley\nAll rights reserved")
        s = self.write("memmove_amd64.s", "// Derived from Inferno's libkern/memmove-386.s\n//\n//\tCopyright © 1994-1999 Lucent Technologies Inc.\n\n#include \"textflag.h\"\n")
        self.assertIn("Lucent Technologies", notice_comment(s))

    def test_a_holder_the_licence_names_adds_nothing_and_one_text_prints_once(self):
        own = self.write("own.go", "// Copyright 2017 The Go Authors. All rights reserved.\n\npackage x\n")
        a = self.write("a.go", "// SPDX-FileCopyrightText: 2026 The Pion community <https://pion.ly>\n// SPDX-License-Identifier: MIT\n\npackage x\n")
        b = self.write("b.go", "// SPDX-FileCopyrightText: 2026 The Pion community <https://pion.ly>\n// SPDX-License-Identifier: MIT\n\npackage x\n")
        got = file_notices([own, a, b], Path(self.dir.name), GO_LICENCE)
        self.assertEqual([n for n, _ in got], ["a.go and 1 more (the file's own notice)"])

    def test_no_notice(self):
        self.assertEqual(notice_comment(self.write("plain.go", "package x\n\nfunc f() {}\n")), "")


if __name__ == "__main__":
    unittest.main()
