# /// script
# requires-python = ">=3.12"
# dependencies = ["playwright==1.62.0"]
# ///
"""Exercise a running test API/workbench using synthetic, persisted packages.

Set CONDUCTOR_BROWSER_API_URL and CONDUCTOR_BROWSER_WEB_URL explicitly. Use an
isolated test database: the API deliberately has no history-deletion operation.
"""
import hashlib
import json
import os
from pathlib import Path
import re
import urllib.request
import uuid

from playwright.sync_api import expect, sync_playwright

api_url = os.environ["CONDUCTOR_BROWSER_API_URL"].rstrip("/")
web_url = os.environ["CONDUCTOR_BROWSER_WEB_URL"]
repository = "synthetic/browser-" + uuid.uuid4().hex
text = "# Synthetic design\nReview the exact saved revision.\n"
content = {
    "intent": {"title": "Shared review acceptance"},
    "futureField": {"retained": True},
    "repositoryContext": {
        "schemaVersion": 1, "repository": repository, "commit": "a" * 40,
        "requestedRef": "main", "collectedAt": "2026-09-12T00:00:00Z",
        "collector": "conductor-git/v1", "artifacts": [
            {"path": "specs/design.md", "state": "collected", "blobOID": "b" * 40,
             "digest": hashlib.sha256(text.encode()).hexdigest(), "text": text},
            {"path": "docs/missing.md", "state": "missing", "message": "Not present at the inspected commit."},
        ],
    },
}


def command(path, actor, body=None):
    request = urllib.request.Request(
        api_url + "/api/v1" + path,
        data=None if body is None else json.dumps(body).encode(),
        headers={"Content-Type": "application/json", "X-Conductor-Actor": actor},
    )
    with urllib.request.urlopen(request, timeout=10) as response:
        return json.load(response)


created = command("/changes", "developer", {"content": content})
change = "/changes/" + created["id"]
command(change + "/review-requests", "developer", {"revision": 1})

with sync_playwright() as playwright:
    browser = playwright.chromium.launch(
        executable_path=os.environ.get("CONDUCTOR_CHROME", "/usr/bin/google-chrome"),
        headless=True,
    )
    page = browser.new_page(viewport={"width": 1440, "height": 1050})
    errors = []
    page.on("pageerror", lambda error: errors.append(str(error)))
    page.goto(web_url)
    page.get_by_label("Repository filter", exact=True).fill(repository)
    page.get_by_role("button", name="Browse shared work", exact=True).click()
    shared = page.get_by_role("region", name="Shared work", exact=True)
    expect(shared.get_by_role("button", name="Inspect change " + created["id"], exact=True)).to_be_visible()
    expect(shared.locator(".shared-card")).to_have_count(1)
    shared.get_by_role("button", name="Inspect change " + created["id"], exact=True).click()
    expect(page.get_by_role("heading", name="Revision 1", exact=True)).to_be_visible()
    expect(page.get_by_role("heading", name="Pinned repository context")).to_be_visible()
    expect(page.get_by_label("Context coverage")).to_contain_text("1 missing")
    page.get_by_role("combobox", name=re.compile("Review perspective")).select_option("QC / QA")
    expect(page.get_by_text("Inspect acceptance criteria, verification plans, and missing or unexecuted evidence.")).to_be_visible()
    approve = page.get_by_role("button", name="Approve inspected revision", exact=True)
    expect(approve).to_be_enabled()

    # Another client edits while the reviewer has revision 1 on screen. Approval
    # must send that old pair, expose 409, and never silently GET newer content.
    content["intent"]["title"] = "Changed by another engineer"
    command(change + "/revisions", "engineer-two", {"expectedRevision": 1, "content": content})
    command(change + "/review-requests", "engineer-two", {"revision": 2})
    requests = []
    page.on("request", lambda request: requests.append((request.method, request.url)))
    approve.click()
    expect(page.get_by_text("Renewed inspection required.", exact=True)).to_be_visible()
    expect(approve).to_be_disabled()
    expect(page.get_by_role("heading", name="Revision 1", exact=True)).to_be_visible()
    assert not any(method == "GET" and "/api/" in url for method, url in requests), requests

    page.get_by_role("button", name="Inspect latest revision", exact=True).click()
    expect(page.get_by_role("heading", name="Revision 2", exact=True)).to_be_visible()
    expect(approve).to_be_enabled()
    page.get_by_role("button", name="Compare revisions", exact=True).click()
    expect(page.get_by_role("heading", name=re.compile("Revision 1 → inspected revision 2"))).to_be_visible()
    expect(page.locator(".comparison")).to_contain_text("Changed by another engineer")
    expect(approve).to_be_enabled()
    approve.click()
    expect(page.get_by_text("DESIGN APPROVED", exact=True)).to_be_visible()

    # The retained approval remains historical after a subsequent edit.
    content["intent"]["title"] = "Third revision awaits review"
    command(change + "/revisions", "developer", {"expectedRevision": 2, "content": content})
    page.get_by_role("button", name="Inspect latest revision", exact=True).click()
    expect(page.get_by_role("heading", name="Revision 3", exact=True)).to_be_visible()
    page.get_by_role("button", name="Inspect historical revision 2", exact=True).click()
    expect(page.get_by_text("HISTORICAL VIEW", exact=True)).to_be_visible()
    expect(page.get_by_role("heading", name="Historical approval records", exact=True)).to_be_visible()
    expect(page.get_by_text("Historical approval for revision 2", exact=True)).to_be_visible()
    expect(approve).to_have_count(0)
    page.get_by_role("button", name="Related work in this repository", exact=True).click()
    expect(shared.get_by_role("button", name="Inspect change " + created["id"], exact=True)).to_be_visible()
    expect(shared.locator(".shared-card")).to_contain_text("Revision 3")
    expect(page.get_by_text("HISTORICAL VIEW", exact=True)).to_be_visible()
    screenshot = Path(os.environ.get("CONDUCTOR_BROWSER_SCREENSHOT", "/tmp/conductor-review-workbench.png"))
    page.screenshot(path=str(screenshot), full_page=True)
    page.set_viewport_size({"width": 390, "height": 844})
    assert page.evaluate("document.documentElement.scrollWidth <= window.innerWidth"), "mobile horizontal overflow"
    page.screenshot(path=str(screenshot.with_name(screenshot.stem + "-mobile.png")), full_page=True)
    assert not errors, errors
    browser.close()
    print("PASS: shared discovery and related work, pinned context, perspective prompts, stale approval, comparison, retained historical approval, mobile layout")
    print(f"Screenshot: {screenshot}")
