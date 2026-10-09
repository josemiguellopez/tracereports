import json
from tracereports import TraceReports

cr = TraceReports()
cr.start_run("ÓrangeHRM / PIM", detect_context=False)
cr.start_test("local HTML")
cr.log_info("renderer evidence")
cr.end_test(status="PASS")
cr.end_run()
print("RESULT " + json.dumps({"report": cr.offline_report, "url": cr.report_url, "run": cr.run_id}))
