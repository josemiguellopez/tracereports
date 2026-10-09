import Reporter from "../../client/js/src/reporter.js";
const reporter = new Reporter({ runName: "ÓrangeHRM / PIM" });
reporter.onBegin({ rootDir: process.cwd(), projects: [] }, { allTests: () => [1] });
await reporter.started;
const test = await reporter.cr.startTest("local HTML");
test.info("renderer evidence"); test.finish("PASS");
await reporter.onEnd({ status: "passed" });
console.log("RESULT " + JSON.stringify({ report: reporter.cr.offlineReport, url: reporter.cr.reportUrl, run: reporter.cr.runId }));
