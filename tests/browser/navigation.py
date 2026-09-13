# /// script
# requires-python = ">=3.12"
# dependencies = ["playwright==1.62.0"]
# ///
"""Signed Chromium navigation: permissions and private state survive no shortcut."""
import os
from urllib.parse import urlparse
from playwright.sync_api import Error, expect, sync_playwright

origin = os.environ["CONDUCTOR_BROWSER_WEB_URL"].rstrip("/")
assert urlparse(origin).scheme == "https" and urlparse(origin).hostname in {"127.0.0.1", "::1"}
change = os.environ["CONDUCTOR_BROWSER_CHANGE_ID"]
with sync_playwright() as p:
    browser = p.chromium.launch(executable_path=os.environ.get("CONDUCTOR_CHROME", "/usr/bin/google-chrome"), headless=True)
    context = browser.new_context(ignore_https_errors=True, viewport={"width": 1440, "height": 1000})
    page = context.new_page(); page.set_default_timeout(10000)
    errors = []; page.on("pageerror", lambda error: errors.append(str(error)))
    page.goto(origin); page.get_by_role("link", name="Sign in", exact=True).click()
    page.get_by_role("button", name="Sign in as reader", exact=True).click()
    page.get_by_label("Workspace", exact=True).select_option("team")
    page.get_by_label("Managed repository", exact=True).select_option("application")
    tabs = page.get_by_role("tablist", name="Engineering workflows")
    expect(tabs.get_by_role("tab")).to_have_count(6)
    expect(page.get_by_role("tabpanel")).to_have_count(1)
    expect(page.get_by_role("tab", name="Review", exact=True)).to_have_attribute("aria-selected", "true")
    page.get_by_label("Change ID", exact=True).fill(change)
    page.get_by_role("button", name="Inspect latest revision", exact=True).click()
    expect(page.get_by_role("heading", name="Revision 1", exact=True)).to_be_visible()
    expect(page.get_by_role("button", name="Inspect latest revision", exact=True)).to_be_enabled()
    expect(page.get_by_role("button", name="Approve inspected revision", exact=True)).to_be_disabled()
    # Perspective and keyboard navigation are local guidance, never authority or
    # a concealed discovery/refresh operation that changes the inspected tuple.
    requests = []; capture = lambda request: requests.append(request.url) if "/api/" in request.url else None
    page.on("request", capture)
    for perspective in ["Architect", "QC / QA", "Developer", "Product", "Operations"]:
        page.get_by_label("Review perspective", exact=True).select_option(perspective)
        expect(page.get_by_role("button", name="Approve inspected revision", exact=True)).to_be_disabled()
    expect(page.get_by_role("region", name="Review perspective guidance")).to_contain_text("recovery, observability")
    review = page.get_by_role("tab", name="Review", exact=True)
    review.focus(); page.keyboard.press("ArrowRight")
    expect(page.get_by_role("tab", name="Source & graph", exact=True)).to_be_focused()
    expect(page.get_by_role("tabpanel", name="Source & graph", exact=True)).to_be_visible()
    page.keyboard.press("End")
    expect(page.get_by_role("tab", name="Runtime", exact=True)).to_be_focused()
    page.keyboard.press("ArrowRight")
    expect(review).to_be_focused()
    page.keyboard.press("ArrowLeft")
    expect(page.get_by_role("tab", name="Runtime", exact=True)).to_be_focused()
    page.keyboard.press("Home")
    expect(review).to_be_focused()
    expect(page.get_by_role("heading", name="Revision 1", exact=True)).to_be_visible()
    assert requests == [], requests
    page.remove_listener("request", capture)
    # A response held after the server read cannot repopulate an interrupted view.
    held = []; path = origin + "/api/v1/changes/" + change
    def hold_read(route):
        held.append((route, route.fetch()))
        page.evaluate("window.navigationReadCaptured = true")
    page.route(path, hold_read)
    page.get_by_role("button", name="Inspect latest revision", exact=True).click()
    expect(page.get_by_role("button", name="Inspect latest revision", exact=True)).to_be_disabled()
    page.wait_for_function("window.navigationReadCaptured === true")
    page.get_by_role("tab", name="Runtime", exact=True).click()
    assert len(held) == 1
    try:
        held[0][0].fulfill(response=held[0][1])
    except Error as error:
        assert any(word in str(error).lower() for word in ["cancel", "closed", "already handled"]), str(error)
    page.unroute(path)
    review.click()
    expect(page.get_by_role("heading", name="Revision 1", exact=True)).to_have_count(0)
    expect(page.get_by_text("Renewed inspection required.", exact=True)).to_be_visible()
    page.get_by_role("button", name="Inspect latest revision", exact=True).click()
    expect(page.get_by_role("heading", name="Revision 1", exact=True)).to_be_visible()
    # A canonical scope change clears every retained workflow, including one
    # currently hidden, and resets the local perspective with the workbench.
    page.get_by_role("tab", name="Runtime", exact=True).click()
    page.get_by_label("Managed repository", exact=True).select_option("private")
    expect(page.get_by_label("Change ID", exact=True)).to_have_value("")
    expect(page.get_by_label("Review perspective", exact=True)).to_have_value("Architect")
    expect(page.get_by_text("Synthetic private navigation inspection", exact=True)).to_have_count(0)
    page.get_by_label("Managed repository", exact=True).select_option("application")
    expect(page.get_by_label("Change ID", exact=True)).to_have_value("")
    page.get_by_role("tab", name="Runtime", exact=True).click()
    page.set_viewport_size({"width":390,"height":844})
    for name in ["Review", "Source & graph", "Agent work", "Delivery", "Tracker", "Runtime"]:
        page.get_by_role("tab", name=name, exact=True).click()
        expect(page.get_by_role("tabpanel")).to_have_count(1)
        assert page.evaluate("document.documentElement.scrollWidth <= innerWidth"), name
    page.evaluate("window.scrollTo(0, 0)")
    page.screenshot(path="/tmp/conductor-navigation-mobile.png", full_page=True)
    page.set_viewport_size({"width":1440,"height":1000})
    page.evaluate("window.scrollTo(0, 0)")
    page.screenshot(path="/tmp/conductor-navigation.png", full_page=True)
    # Decisive access denial removes all tabs and their hidden private state.
    page.route(origin + "/api/v1/runtime-evidence?*", lambda route: route.fulfill(status=403, body=""))
    page.get_by_role("button", name="Refresh runtime evidence", exact=True).click()
    expect(page.get_by_role("heading", name="Check your access", exact=True)).to_be_visible()
    expect(page.get_by_role("tab")).to_have_count(0)
    expect(page.locator('[role="tabpanel"]')).to_have_count(0)
    page.get_by_role("button", name="Reload session", exact=True).click()
    page.get_by_label("Workspace", exact=True).select_option("team")
    page.get_by_label("Managed repository", exact=True).select_option("application")
    expect(page.get_by_label("Change ID", exact=True)).to_have_value("")
    assert page.evaluate("localStorage.length + sessionStorage.length") == 0
    assert not errors, errors
    context.close(); browser.close()
print("PASS: six keyboard/mobile workflows, Operations and permission-free perspectives, interrupted-read fencing, hidden-state scope clearing, access-denial recovery and screenshots")
