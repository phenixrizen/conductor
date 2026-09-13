# /// script
# requires-python = ">=3.12"
# dependencies = ["playwright==1.62.0"]
# ///
"""Actual browser decisions over shared MCP-authored plans and PostgreSQL."""
import json
import os
from pathlib import Path
import ssl
import urllib.request
from urllib.parse import urlparse
from playwright.sync_api import expect, sync_playwright
origin=os.environ["CONDUCTOR_BROWSER_WEB_URL"].rstrip("/")
assert urlparse(origin).scheme=="https" and urlparse(origin).hostname in {"127.0.0.1","::1"}
run_id=os.environ["CONDUCTOR_BROWSER_RUN_ID"]

def sign_in(page,who):
    page.goto(origin)
    page.get_by_role("link",name="Sign in",exact=True).click()
    page.get_by_role("button",name="Sign in as "+who,exact=True).click()
    expect(page.locator(".signed-in-identity")).to_contain_text("person-"+who)
    page.get_by_label("Workspace",exact=True).select_option("team")
    page.get_by_label("Managed repository",exact=True).select_option("application")
    page.get_by_role("tab", name="Agent work", exact=True).click()

def author_revise():
    request=urllib.request.Request(origin+"/api/v1/changes/"+os.environ["CONDUCTOR_BROWSER_STALE_PACKAGE"]+"/revisions",data=json.dumps({"expectedRevision":1,"content":{"intent":"New design revision invalidates execution pins"}}).encode(),headers={"Content-Type":"application/json","Authorization":"Bearer "+os.environ["CONDUCTOR_BROWSER_AUTHOR_TOKEN"],"X-Conductor-Workspace":"team","X-Conductor-Repository":"application"})
    with urllib.request.urlopen(request,context=ssl._create_unverified_context(),timeout=10) as response: assert response.status==201

with sync_playwright() as p:
    browser=p.chromium.launch(executable_path=os.environ.get("CONDUCTOR_CHROME","/usr/bin/google-chrome"),headless=True)
    context=browser.new_context(ignore_https_errors=True,viewport={"width":1440,"height":1100})
    page=context.new_page();page.set_default_timeout(10000)
    errors=[];page.on("pageerror",lambda e:errors.append(str(e)))
    sign_in(page,"reviewer");panel=page.locator(".coordinated-runs")
    panel.get_by_role("button",name="Refresh runs and access",exact=True).click()
    panel.get_by_role("button",name="Inspect run "+run_id,exact=True).click()
    inspected=panel.get_by_role("article",name="Inspected coordinated run")
    expect(inspected).to_contain_text("person-agent")
    expect(inspected).to_contain_text(os.environ["CONDUCTOR_BROWSER_RUN_DIGEST"])
    expect(inspected).to_contain_text("No receipt; verification unknown")
    panel.get_by_role("button",name="Authorize inspected plan",exact=True).click()
    dialog=panel.get_by_role("dialog",name="Confirm execution decision")
    expect(dialog).to_contain_text(os.environ["CONDUCTOR_BROWSER_RUN_DIGEST"])
    # Leaving a workflow dismisses an unsubmitted decision. Returning cannot
    # resurrect it or perform a hidden access/plan refresh.
    navigation_requests=[]
    observe_navigation=lambda request:navigation_requests.append(request.url) if "/api/" in request.url else None
    page.on("request",observe_navigation)
    page.get_by_role("tab",name="Runtime",exact=True).click()
    page.get_by_role("tab",name="Agent work",exact=True).click()
    expect(dialog).to_have_count(0)
    expect(inspected).to_contain_text(os.environ["CONDUCTOR_BROWSER_RUN_DIGEST"])
    assert navigation_requests==[],navigation_requests
    page.remove_listener("request",observe_navigation)
    panel.get_by_role("button",name="Authorize inspected plan",exact=True).click()
    expect(dialog).to_be_visible()
    author_revise()
    requests=[]
    def capture(request):
        if "/api/" in request.url:requests.append((request.method,urlparse(request.url).path,request.post_data_json if request.method=="POST" else None))
    page.on("request",capture)
    panel.get_by_role("button",name="Confirm execution authorization",exact=True).click()
    expect(panel.get_by_text("Renewed run inspection required before another execution command.",exact=True)).to_be_visible()
    assert requests==[("POST","/api/v1/coordination-runs/"+run_id+"/authorization",{"digest":os.environ["CONDUCTOR_BROWSER_RUN_DIGEST"]})],requests
    page.remove_listener("request",capture)
    expect(panel.get_by_role("button",name="Authorize inspected plan",exact=True)).to_be_disabled()
    panel.locator("summary").filter(has_text="Import or author a plan proposal").click()
    panel.get_by_label("Plan JSON",exact=True).fill('{"schemaVersion":1,"schemaVersion":2}')
    panel.get_by_role("button",name="Preview plan",exact=True).click()
    expect(panel.get_by_role("alert").filter(has_text="Duplicate JSON field")).to_be_visible()
    panel.get_by_label("Plan JSON file",exact=True).set_input_files(os.environ["CONDUCTOR_BROWSER_PLAN_FILE"])
    expect(panel.get_by_role("region",name="Plan proposal preview")).to_be_visible()
    proposals=[]
    def lose_proposal(route):
        response=route.fetch();assert response.status==201,response.text()
        proposals.append((route.request.post_data_json,route.request.headers["idempotency-key"],response.json()))
        route.abort()
    proposal_url=origin+"/api/v1/coordination-runs"
    page.route(proposal_url,lose_proposal);requests.clear();page.on("request",capture)
    panel.get_by_role("button",name="Record plan proposal",exact=True).click()
    expect(panel.get_by_role("button",name="Retry exact plan proposal",exact=True)).to_be_visible()
    assert len(proposals)==1 and len(requests)==1 and requests[0][0]=="POST",requests
    page.unroute(proposal_url,lose_proposal)
    with page.expect_request(lambda r:r.method=="POST" and r.url==proposal_url) as retry:
        panel.get_by_role("button",name="Retry exact plan proposal",exact=True).click()
    expect(panel.get_by_text("Shared proposal recorded.",exact=False)).to_be_visible()
    assert retry.value.post_data_json==proposals[0][0] and retry.value.headers["idempotency-key"]==proposals[0][1]
    assert len(requests)==2 and all(r[0]=="POST" for r in requests),requests
    page.remove_listener("request",capture)
    created=proposals[0][2]
    assert page.evaluate("window.untrustedExecuted === undefined")
    panel.get_by_role("button",name="Authorize inspected plan",exact=True).click()
    authorizations=[]
    authorization_url=proposal_url+"/"+created["id"]+"/authorization"
    def lose_authorization(route):
        response=route.fetch();assert response.status==202,response.text()
        authorizations.append(response.json());route.abort()
    page.route(authorization_url,lose_authorization);requests.clear();page.on("request",capture)
    panel.get_by_role("button",name="Confirm execution authorization",exact=True).click()
    expect(panel.get_by_text("Renewed run inspection required before another execution command.",exact=True)).to_be_visible()
    assert len(authorizations)==1 and requests==[("POST",urlparse(authorization_url).path,{"digest":created["digest"]})],requests
    page.remove_listener("request",capture);page.unroute(authorization_url,lose_authorization)
    panel.get_by_role("button",name="Refresh inspected run",exact=True).click()
    expect(panel.get_by_role("button",name="Request run cancellation",exact=True)).to_be_enabled()
    expect(panel.get_by_role("button",name="Authorize inspected plan",exact=True)).to_be_disabled()
    panel.get_by_role("button",name="Request run cancellation",exact=True).click()
    requests.clear();page.on("request",capture)
    panel.get_by_role("button",name="Confirm run cancellation",exact=True).click()
    expect(panel.get_by_text("Cancellation intent recorded.",exact=False)).to_be_visible()
    assert requests==[("POST","/api/v1/coordination-runs/"+created["id"]+"/cancellation",{"digest":created["digest"]})],requests
    page.remove_listener("request",capture)
    expect(panel.get_by_role("region",name="Coordinated execution observation")).to_contain_text("Progress unknown")
    page.evaluate("window.scrollTo(0, 0)")
    page.screenshot(path="/tmp/conductor-coordination-workbench.png",full_page=True)
    page.set_viewport_size({"width":390,"height":844})
    assert page.evaluate("document.documentElement.scrollWidth <= innerWidth"),"mobile coordination overflow"
    page.evaluate("window.scrollTo(0, 0)")
    page.screenshot(path="/tmp/conductor-coordination-workbench-mobile.png",full_page=True)
    page.get_by_label("Managed repository",exact=True).select_option("")
    expect(page.get_by_role("article",name="Inspected coordinated run")).to_have_count(0)
    expect(page.get_by_role("dialog",name="Confirm execution decision")).to_have_count(0)
    context.close()
    for identity in ["reader"]:
        other=browser.new_context(ignore_https_errors=True);viewer=other.new_page();sign_in(viewer,identity)
        shared=viewer.locator(".coordinated-runs");shared.get_by_role("button",name="Refresh runs and access",exact=True).click()
        shared.get_by_label("Run ID",exact=True).fill(created["id"]);shared.get_by_role("button",name="Inspect shared run",exact=True).click()
        expect(shared.get_by_role("article",name="Inspected coordinated run")).to_contain_text(created["digest"])
        expect(shared.get_by_role("button",name="Authorize inspected plan",exact=True)).to_have_count(0)
        expect(shared.get_by_role("button",name="Request run cancellation",exact=True)).to_have_count(0)
        other.close()
    agent_context=browser.new_context(ignore_https_errors=True)
    agent_page=agent_context.new_page();agent_page.goto(origin)
    agent_page.get_by_role("link",name="Sign in",exact=True).click()
    agent_page.get_by_role("button",name="Sign in as agent",exact=True).click()
    expect(agent_page.get_by_role("heading",name="Sign-in could not be completed",exact=True)).to_be_visible()
    agent_context.close()
    assert not errors,errors
    browser.close()
    print("PASS: shared MCP agent proposal, source/profile/task inspection, changed-package authorization rejection, duplicate JSON denial, file preview, exact proposal retry, uncertain authorization recovery, exact cancellation, explicit unexecuted evidence, read-only/agent controls, scope clearing and desktop/mobile screenshots")
