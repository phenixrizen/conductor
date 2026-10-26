"""Tests for release_draft.py: python3 -m unittest discover -s scripts -p '*_test.py'"""
from pathlib import Path
import tempfile
import unittest

from release_draft import built, plan, problems


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


class Check(unittest.TestCase):
    PACKAGES = ["Conductor-0.7.0-rc.17.dmg", "latest-mac.yml", "Conductor-Setup-0.7.0-rc.17.exe", "latest.yml"]

    def test_one_draft_with_every_package_passes(self):
        r = release(7, "v0.7.0-rc.17", assets=self.PACKAGES + ["Conductor-0.7.0-rc.17.dmg.blockmap"])
        self.assertEqual(problems([r], "v0.7.0-rc.17", self.PACKAGES), [])

    def test_packages_split_over_two_drafts_fail(self):
        a = release(7, "v0.7.0-rc.17", assets=self.PACKAGES[:2])
        b = release(8, "v0.7.0-rc.17", assets=self.PACKAGES[2:])
        self.assertEqual(problems([a, b], "v0.7.0-rc.17", self.PACKAGES), ["v0.7.0-rc.17 has 2 releases (7, 8), not one draft"])

    def test_a_package_missing_from_the_draft_is_named(self):
        r = release(7, "v0.7.0-rc.17", assets=self.PACKAGES[:3])
        self.assertEqual(problems([r], "v0.7.0-rc.17", self.PACKAGES), ["latest.yml is not on v0.7.0-rc.17's release 7"])

    def test_an_upload_not_finished_is_not_on_the_release(self):
        r = release(7, "v0.7.0-rc.17", assets=self.PACKAGES)
        r["assets"][3]["state"] = "starter"
        self.assertEqual(problems([r], "v0.7.0-rc.17", self.PACKAGES), ["latest.yml is not on v0.7.0-rc.17's release 7"])

    def test_no_release_fails(self):
        self.assertEqual(problems([], "v0.7.0-rc.17", self.PACKAGES), ["v0.7.0-rc.17 has 0 releases (none), not one draft"])

    def test_a_published_release_fails(self):
        r = release(7, "v0.7.0-rc.17", draft=False, assets=self.PACKAGES)
        self.assertEqual(problems([r], "v0.7.0-rc.17", self.PACKAGES), ["v0.7.0-rc.17's release 7 is not a draft"])

    def test_the_packages_are_the_artifacts_files_without_electron_builders_logs(self):
        with tempfile.TemporaryDirectory() as d:
            for name in ["Conductor-0.7.0-rc.17.dmg", "latest-mac.yml", "builder-debug.yml", "sub/latest.yml"]:
                p = Path(d, name)
                p.parent.mkdir(parents=True, exist_ok=True)
                p.write_text("x")
            self.assertEqual(built(Path(d)), ["Conductor-0.7.0-rc.17.dmg", "latest-mac.yml", "latest.yml"])


if __name__ == "__main__":
    unittest.main()
