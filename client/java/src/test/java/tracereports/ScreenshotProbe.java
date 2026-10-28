package tracereports;

/** Proceso aparte para MaskTest: envía una captura INFO y una FAIL con el TRACEREPORTS_SCREENSHOTS del entorno. */
public final class ScreenshotProbe {
    private ScreenshotProbe() {}

    public static void main(String[] args) {
        TraceReports cr = new TraceReports(args[0], "");
        cr.startRun(new TraceReports.RunInfo("capturas"));
        TraceTest t = cr.startTest("t");
        byte[] png = {(byte) 0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0};
        t.screenshot(png, "paso", Status.INFO);
        t.screenshot(png, "fallo", Status.FAIL);
        t.finish(Status.FAIL);
        cr.finishRun();
    }
}
