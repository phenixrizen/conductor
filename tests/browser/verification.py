# /// script
# requires-python = ">=3.12"
# dependencies = ["playwright==1.62.0"]
# ///
"""Exact criterion support is bounded inspection, never all-requirements approval."""
import os
from urllib.parse import urlparse
from playwright.sync_api import expect, sync_playwright

expect.set_options(timeout=15000)
origin = os.environ["CONDUCTOR_BROWSER_WEB_URL"].rstrip("/")
assert urlparse(origin).scheme == "https" and urlparse(origin).hostname in {"127.0.0.1", "::1"}
with sync_playwright() as p:
    browser = p.chromium.launch(executable_path=os.environ.get("CONDUCTOR_CHROME", "/usr/bin/google-chrome"), headless=True)
    context = browser.new_context(ignore_https_errors=True, viewport={"width":1440,"height":1100})
    page = context.new_page()
    errors = []; page.on("pageerror", lambda e: errors.append(str(e)))
    page.goto(origin); page.get_by_role("link", name="Sign in", exact=True).click()
    page.get_by_role("button", name="Sign in as reader", exact=True).click()
    page.get_by_label("Workspace", exact=True).select_option("team")
    page.get_by_label("Managed repository", exact=True).select_option("application")
    page.get_by_role("tab", name="Agent work", exact=True).click()
    panel = page.locator(".coordinated-runs")
    panel.get_by_label("Run ID", exact=True).fill(os.environ["CONDUCTOR_BROWSER_CRITERIA_RUN"])
    panel.get_by_role("button", name="Inspect shared run", exact=True).click()
    expect(panel.get_by_label("Criterion links for test", exact=True)).to_contain_text("synthetic-output")
    panel.get_by_role("button", name="Inspect task artifact code", exact=True).click()
    artifact = panel.get_by_role("region", name="Inspected task artifact", exact=True)
    support = artifact.get_by_label("Criterion support", exact=True)
    expect(support).to_contain_text("synthetic-output · supported")
    expect(support).to_contain_text("unlinked · not_verified")
    expect(support).to_contain_text("revision 1")
    expect(artifact).to_contain_text("This does not verify all requirements")
    assert page.evaluate("window.criterionInjected === undefined")
    page.screenshot(path="/tmp/conductor-verification-criteria.png", full_page=True)
    page.set_viewport_size({"width":390,"height":844})
    assert page.evaluate("document.documentElement.scrollWidth <= innerWidth"), "criterion evidence overflow"
    page.screenshot(path="/tmp/conductor-verification-criteria-mobile.png", full_page=True)
    def substitute_revision(value):
        value["verification"]["criteria"][0]["requirement"]["revision"] += 1
    def invented_check(value):
        criterion = value["verification"]["criteria"][1]
        criterion["state"] = "supported"; criterion["checkIds"] = ["invented"]
    def failed_check(value):
        value["artifact"]["checks"][0]["state"] = "failed"
    for mutation in [substitute_revision, invented_check, failed_check]:
        def substitute(route):
            response = route.fetch(); value = response.json(); mutation(value)
            route.fulfill(response=response, json=value)
        page.route("**/coordination-runs/*/artifact?*", substitute)
        panel.get_by_role("button", name="Inspect task artifact code", exact=True).click()
        expect(panel.get_by_role("alert")).to_contain_text("Criterion support does not match")
        expect(artifact).to_have_count(0)
        page.unroute("**/coordination-runs/*/artifact?*", substitute)
    assert not errors, errors
    context.close(); browser.close()
print("Criterion browser acceptance passed: exact linked support, unlinked not_verified, escaped descriptions and substituted revision, invented checks and contradicted passing evidence denial.")
