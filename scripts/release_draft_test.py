"""Tests for release_draft.py: python3 -m unittest discover -s scripts -p '*_test.py'"""
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock

import release_draft
from release_draft import built, draft, plan, problems


def release(rid, tag, draft=True, assets=()):
    return {"id": rid, "tag_name": tag, "draft": draft, "assets": [{"name": a, "state": "uploaded"} for a in assets]}


class Ensure(unittest.TestCase):
    def test_a_tag_with_no_release_gets_one_made(self):
        self.assertEqual(plan([release(1, "v0.7.0-rc.16", draft=False)], "v0.7.0-rc.17"), ("create", None))

    def test_a_rerun_keeps_the_draft_the_first_run_made(self):
        self.assertEqual(plan([release(7, "v0.7.0-rc.17")], "v0.7.0-rc.17"), ("keep", 7))

    def test_a_release_tagged_with_the_version_is_the_tags_as_electron_builder_takes_it(self):
        self.assertEqual(plan([release(7, "0.7.0-rc.17")], "v0.7.0-rc.17"), ("keep", 7))

    def test_a_published_release_is_not_built_into(self):
        with self.assertRaisesRegex(ValueError, "published release \\(3\\)"):
            plan([release(3, "v0.7.0-rc.17", draft=False)], "v0.7.0-rc.17")

    def test_two_drafts_are_refused_with_their_ids(self):
        with self.assertRaisesRegex(ValueError, "2 draft releases \\(4, 5\\)"):
            plan([release(4, "v0.7.0-rc.17"), release(5, "v0.7.0-rc.17")], "v0.7.0-rc.17")


class Upload(unittest.TestCase):
    def test_upload_takes_the_tags_one_draft(self):
        self.assertEqual(draft([release(1, "v0.7.0-rc.16", draft=False), release(7, "v0.7.0-rc.17")], "v0.7.0-rc.17")["id"], 7)

    def test_upload_never_makes_a_release(self):
        with self.assertRaisesRegex(ValueError, "no draft release"):
            draft([], "v0.7.0-rc.17")

    def test_upload_refuses_two_drafts_and_a_published_release(self):
        for rs in ([release(4, "v0.7.0-rc.17"), release(5, "v0.7.0-rc.17")], [release(3, "v0.7.0-rc.17", draft=False)]):
            with self.assertRaises(ValueError):
                draft(rs, "v0.7.0-rc.17")


class Check(unittest.TestCase):
    # rc.17's names on its release, the Windows installer as electron-builder published it.
    PACKAGES = ["Conductor-0.7.0-rc.17.dmg", "Conductor-0.7.0-rc.17.dmg.blockmap", "latest-mac.yml",
                "Conductor-Setup-0.7.0-rc.17.exe", "Conductor-Setup-0.7.0-rc.17.exe.blockmap", "latest.yml",
                "Conductor-0.7.0-rc.17.AppImage", "conductor-desktop_0.7.0-rc.17_amd64.deb", "latest-linux.yml"]

    def test_one_draft_with_every_package_passes(self):
        r = release(7, "v0.7.0-rc.17", assets=self.PACKAGES)
        self.assertEqual(problems([r], "v0.7.0-rc.17", self.PACKAGES), [])

    def test_an_installer_without_its_blockmap_fails(self):
        on = [p for p in self.PACKAGES if p != "Conductor-Setup-0.7.0-rc.17.exe.blockmap"]
        r = release(7, "v0.7.0-rc.17", assets=on)
        self.assertEqual(problems([r], "v0.7.0-rc.17", on), ["Conductor-Setup-0.7.0-rc.17.exe has no Conductor-Setup-0.7.0-rc.17.exe.blockmap on v0.7.0-rc.17's release 7"])

    def test_packages_split_over_two_drafts_fail(self):
        a = release(7, "v0.7.0-rc.17", assets=self.PACKAGES[:5])
        b = release(8, "v0.7.0-rc.17", assets=self.PACKAGES[5:])
        self.assertEqual(problems([a, b], "v0.7.0-rc.17", self.PACKAGES), ["v0.7.0-rc.17 has 2 releases (7, 8), not one draft"])

    def test_a_package_missing_from_the_draft_is_named(self):
        r = release(7, "v0.7.0-rc.17", assets=[p for p in self.PACKAGES if p != "latest.yml"])
        self.assertEqual(problems([r], "v0.7.0-rc.17", self.PACKAGES), ["latest.yml is not on v0.7.0-rc.17's release 7"])

    def test_an_upload_not_finished_is_not_on_the_release(self):
        r = release(7, "v0.7.0-rc.17", assets=self.PACKAGES)
        r["assets"][5]["state"] = "starter"
        self.assertEqual(problems([r], "v0.7.0-rc.17", self.PACKAGES), ["latest.yml is not on v0.7.0-rc.17's release 7"])

    def test_no_release_fails(self):
        self.assertEqual(problems([], "v0.7.0-rc.17", self.PACKAGES), ["v0.7.0-rc.17 has 0 releases (none), not one draft"])

    def test_a_published_release_fails(self):
        r = release(7, "v0.7.0-rc.17", draft=False, assets=self.PACKAGES)
        self.assertEqual(problems([r], "v0.7.0-rc.17", self.PACKAGES), ["v0.7.0-rc.17's release 7 is not a draft"])

    def test_the_packages_are_a_builds_published_files_at_its_top(self):
        with tempfile.TemporaryDirectory() as d:
            for name in ["Conductor-Setup-0.7.0-rc.17.exe", "Conductor-Setup-0.7.0-rc.17.exe.blockmap", "latest.yml",
                         "builder-debug.yml", "builder-effective-config.yaml", "conductor-desktop-0.7.0-rc.17-x64.nsis.7z",
                         "win-unpacked/Conductor.exe", "mac/Conductor.app/Contents/Info.plist"]:
                p = Path(d, name)
                p.parent.mkdir(parents=True, exist_ok=True)
                p.write_text("x")
            self.assertEqual(built(Path(d)), ["Conductor-Setup-0.7.0-rc.17.exe", "Conductor-Setup-0.7.0-rc.17.exe.blockmap", "latest.yml"])


class Gh(unittest.TestCase):
    def test_ghs_output_is_read_as_utf8_whatever_the_locale(self):
        line = '{"id": 7, "tag_name": "v0.7.0-rc.17", "draft": true, "assets": [], "note": "\u201cquoted\u201d"}\n'
        def run(argv, **kw):
            # As subprocess decodes: the bytes gh wrote, with the encoding asked for (the locale's if none).
            return subprocess.CompletedProcess(argv, 0, stdout=line.encode("utf-8").decode(kw.get("encoding") or "cp1252"))
        with mock.patch.object(release_draft.subprocess, "run", side_effect=run):
            self.assertEqual(release_draft.releases("o/r")[0]["note"], "\u201cquoted\u201d")

    def test_releases_are_read_without_their_notes(self):
        with mock.patch.object(release_draft, "gh", return_value="") as gh:
            release_draft.releases("o/r")
        self.assertNotIn("body", gh.call_args.args[-1])
        self.assertIn("{id, tag_name, draft, assets:", gh.call_args.args[-1])


if __name__ == "__main__":
    unittest.main()
