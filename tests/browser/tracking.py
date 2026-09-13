# /// script
# requires-python = ">=3.12"
# dependencies = ["playwright==1.62.0"]
# ///
"""Signed browser workflows over real Linear/Jira HTTP adapters and shared facts."""
import os
from urllib.parse import urlparse
from playwright.sync_api import expect,sync_playwright
origin=os.environ["CONDUCTOR_BROWSER_WEB_URL"].rstrip("/")
assert urlparse(origin).scheme=="https" and urlparse(origin).hostname in {"127.0.0.1","::1"}
provider=os.environ["CONDUCTOR_BROWSER_TRACKER_PROVIDER"]
link_id=os.environ["CONDUCTOR_BROWSER_TRACKER_LINK_ID"]

def sign_in(page,who,hint=False):
    page.goto(origin+("/?tracker-link="+link_id if hint else ""))
    page.get_by_role("link",name="Sign in",exact=True).click()
    page.get_by_role("button",name="Sign in as "+who,exact=True).click()
    expect(page.locator(".signed-in-identity")).to_contain_text("person-"+who)
    page.get_by_label("Workspace",exact=True).select_option("team")
    page.get_by_label("Managed repository",exact=True).select_option("application")

with sync_playwright() as p:
    browser=p.chromium.launch(executable_path=os.environ.get("CONDUCTOR_CHROME","/usr/bin/google-chrome"),headless=True)
    context=browser.new_context(ignore_https_errors=True,viewport={"width":1440,"height":1100})
    page=context.new_page();page.set_default_timeout(10000)
    errors=[];page.on("pageerror",lambda e:errors.append(str(e)))
    sign_in(page,"reviewer",True);panel=page.locator(".work-tracking")
    expect(panel.get_by_label("Tracker link ID",exact=True)).to_have_value(link_id)
    panel.get_by_role("button",name="Refresh tracker and links",exact=True).click()
    expect(panel.get_by_role("region",name="Configured workspace tracker")).to_contain_text("Linear" if provider=="linear" else "Jira")
    panel.get_by_role("button",name="Inspect shared tracker link",exact=True).click()
    inspected=panel.get_by_role("article",name="Inspected tracker link")
    expect(inspected).to_contain_text(os.environ["CONDUCTOR_BROWSER_TRACKER_LINK_DIGEST"])
    expect(inspected).to_contain_text("person-agent")
    expect(inspected).to_contain_text("Tracker planning done")
    expect(inspected).to_contain_text("does not establish effective approval")
    panel.locator("summary").filter(has_text="Link existing ticket to exact work").click()
    panel.get_by_label("Tracker link JSON",exact=True).fill('{"issueId":"one","issueId":"two"}')
    panel.get_by_role("button",name="Preview tracker link",exact=True).click()
    expect(panel.get_by_role("alert")).to_contain_text("Duplicate JSON field")
    panel.get_by_label("Tracker link JSON file",exact=True).set_input_files(os.environ["CONDUCTOR_BROWSER_TRACKER_FILE"])
    expect(panel.get_by_role("region",name="Tracker link preview")).to_be_visible()
    commands=[];created=[]
    def capture(request):
        if "/api/" in request.url:commands.append((request.method,urlparse(request.url).path,request.post_data_json if request.method=="POST" else None))
    url=origin+"/api/v1/tracker-links"
    def lose_link(route):
        response=route.fetch();assert response.status==201,response.text()
        created.append((route.request.post_data_json,route.request.headers["idempotency-key"],response.json()));route.abort()
    page.route(url,lose_link);page.on("request",capture)
    panel.get_by_role("button",name="Record tracker link",exact=True).click()
    expect(panel.get_by_role("button",name="Retry exact tracker link",exact=True)).to_be_visible()
    assert len(commands)==1 and commands[0][0]=="POST",commands
    page.unroute(url,lose_link)
    with page.expect_request(lambda r:r.method=="POST" and r.url==url) as retry:
        panel.get_by_role("button",name="Retry exact tracker link",exact=True).click()
    expect(panel.get_by_text("Shared tracker link recorded",exact=False)).to_be_visible()
    assert retry.value.post_data_json==created[0][0] and retry.value.headers["idempotency-key"]==created[0][1]
    assert len(commands)==2 and all(c[0]=="POST" for c in commands),commands
    page.remove_listener("request",capture);saved=created[0][2]
    def inspect_until(state):
        for _ in range(20):
            panel.get_by_role("button",name="Refresh inspected tracker link",exact=True).click()
            expect(panel.get_by_role("button",name="Refresh inspected tracker link",exact=True)).to_be_enabled()
            facts=inspected.get_by_role("region",name="Tracker observation",exact=True)
            if facts.count() and state in facts.get_by_role("heading",level=4).inner_text():return
            page.wait_for_timeout(100)
        raise AssertionError("tracker observation never became "+state+"\n"+inspected.inner_text())
    inspect_until("refreshed")
    def synchronize(mode,button,lose=False):
        panel.get_by_role("button",name=button,exact=True).click()
        dialog=panel.get_by_role("dialog",name="Confirm tracker synchronization")
        expect(dialog).to_contain_text(saved["digest"])
        posts=[];sync_url=url+"/"+saved["id"]+"/syncs"
        def lose_ack(route):
            response=route.fetch();assert response.status==202,response.text()
            posts.append((route.request.post_data_json,route.request.headers["idempotency-key"]));route.abort()
        if lose:page.route(sync_url,lose_ack)
        commands.clear();page.on("request",capture)
        with page.expect_request(lambda r:r.method=="POST" and r.url==sync_url) as first:
            panel.get_by_role("button",name="Confirm tracker synchronization",exact=True).click()
        body=first.value.post_data_json
        assert body["mode"]==mode and body["linkDigest"]==saved["digest"]
        if mode=="refresh":assert "expectedProjectionDigest" not in body
        elif mode=="restore":assert len(body["expectedProjectionDigest"])==64
        else:assert body.get("expectedProjectionDigest","")==""
        if lose:
            expect(panel.get_by_role("button",name="Retry exact tracker synchronization",exact=True)).to_be_visible()
            page.unroute(sync_url,lose_ack)
            with page.expect_request(lambda r:r.method=="POST" and r.url==sync_url) as retry:
                panel.get_by_role("button",name="Retry exact tracker synchronization",exact=True).click()
            expect(panel.get_by_text("Synchronization request recorded",exact=False)).to_be_visible()
            assert retry.value.post_data_json==posts[0][0] and retry.value.headers["idempotency-key"]==posts[0][1]
        else:expect(panel.get_by_text("Synchronization request recorded",exact=False)).to_be_visible()
        assert len(commands)==(2 if lose else 1) and all(c[0]=="POST" for c in commands),commands
        page.remove_listener("request",capture)
        expect(panel.get_by_role("button",name="Publish Conductor link",exact=True)).to_be_disabled()
        panel.get_by_role("button",name="Inspect latest tracker synchronization",exact=True).click()
        expect(panel.get_by_role("region",name="Inspected tracker synchronization")).to_be_visible()
    synchronize("publish","Publish Conductor link",True)
    inspect_until("synchronized")
    synchronize("refresh","Refresh tracker-owned fields")
    inspect_until("conflict")
    expect(panel.get_by_role("button",name="Publish Conductor link",exact=True)).to_be_disabled()
    expect(panel.get_by_role("button",name="Resolve inspected link conflict",exact=True)).to_be_enabled()
    inspected.locator("summary").filter(has_text="Observed provider attachment or remote link").click()
    expect(inspected).to_contain_text("External edit <script>")
    assert page.evaluate("window.trackerInjected === undefined")
    page.screenshot(path="/tmp/conductor-tracker-"+provider+".png",full_page=True)
    page.set_viewport_size({"width":390,"height":844})
    assert page.evaluate("document.documentElement.scrollWidth <= innerWidth"),"mobile tracker overflow"
    page.screenshot(path="/tmp/conductor-tracker-"+provider+"-mobile.png",full_page=True)
    synchronize("restore","Resolve inspected link conflict",True)
    inspect_until("synchronized")
    # A denied shared read clears the inspected issue, pins, imported preview and decision.
    page.route(url+"/"+saved["id"],lambda route:route.fulfill(status=403,body=""))
    panel.get_by_role("button",name="Refresh inspected tracker link",exact=True).click()
    expect(page.get_by_role("article",name="Inspected tracker link")).to_have_count(0)
    expect(page.get_by_role("region",name="Tracker link preview")).to_have_count(0)
    expect(page.get_by_role("dialog",name="Confirm tracker synchronization")).to_have_count(0)
    context.close()
    readonly=browser.new_context(ignore_https_errors=True);reader=readonly.new_page();sign_in(reader,"reader")
    shared=reader.locator(".work-tracking");shared.get_by_role("button",name="Refresh tracker and links",exact=True).click()
    shared.get_by_label("Tracker link ID",exact=True).fill(saved["id"]);shared.get_by_role("button",name="Inspect shared tracker link",exact=True).click()
    expect(shared.get_by_role("article",name="Inspected tracker link")).to_contain_text(saved["digest"])
    expect(shared.get_by_role("button",name="Publish Conductor link",exact=True)).to_have_count(0)
    expect(shared.get_by_role("button",name="Resolve inspected link conflict",exact=True)).to_have_count(0)
    reader.get_by_label("Managed repository",exact=True).select_option("")
    expect(reader.get_by_role("article",name="Inspected tracker link")).to_have_count(0)
    readonly.close();assert not errors,errors;browser.close()
    print("PASS:",provider,"shared agent link, OIDC navigation hint, exact package preview, strict JSON/file import, link retry, exact publish/refresh/restore digests without GET, lost acknowledgments, tracker-owned status authority separation, escaped conflicts, reader controls, denial/scope clearing and desktop/mobile screenshots")
