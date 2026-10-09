import tracereports.*;
public class JavaDriver {
    public static void main(String[] args) {
        TraceReports cr = new TraceReports();
        cr.startRun("ÓrangeHRM / PIM", "");
        TraceTest test = cr.startTest("local HTML");
        test.info("renderer evidence"); test.finish(Status.PASS);
        cr.finishRun();
        String report = cr.offlineReport() == null ? "" : cr.offlineReport().toString().replace('\\', '/');
        System.out.println("RESULT {\"report\":\"" + report + "\",\"run\":" + cr.runId() + ",\"url\":\"" + cr.reportUrl().replace('\\', '/') + "\"}");
    }
}
