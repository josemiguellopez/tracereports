package tracereports.junit;

import tracereports.TraceReports;
import tracereports.TraceTest;
import tracereports.Delivery;
import tracereports.Status;
import java.lang.reflect.Method;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.List;
import java.util.Optional;
import org.junit.jupiter.api.extension.AfterTestExecutionCallback;
import org.junit.jupiter.api.extension.BeforeEachCallback;
import org.junit.jupiter.api.extension.ExtensionContext;
import org.junit.jupiter.api.extension.ParameterContext;
import org.junit.jupiter.api.extension.ParameterResolver;
import org.junit.jupiter.api.extension.TestWatcher;

/**
 * Extensión de JUnit 5 para TraceReports.
 *
 * <pre>{@code
 * @ExtendWith(TraceReportsExtension.class)
 * class LoginTest {
 *   @Test void adminEntra(TraceTest t) {          // TraceTest se inyecta (opcional)
 *     t.info("Abrir el login");
 *     SeleniumEvidence.attach(t, driver);            // captura y DOM si falla
 *   }
 * }
 * }</pre>
 *
 * <ul>
 *   <li>Una ejecución por JVM (todas las clases con la extensión reportan en ella); se cierra al terminar.
 *   <li>Un test por método, con identidad estable: {@code paquete.Clase#método} (+ los parámetros
 *       si es {@code @ParameterizedTest}); categorías: los {@code @Tag} y la clase.
 *   <li>Resultado, mensaje y stack trace del fallo (entrada del diagnóstico con IA); los
 *       {@code @Disabled} y las suposiciones fallidas quedan como SKIP.
 *   <li>Los hooks {@link TraceTest#beforeFinish} corren antes de {@code @AfterEach}, con el
 *       navegador todavía abierto.
 * </ul>
 *
 * <p>Configuración por variables: TRACEREPORTS_URL, TRACEREPORTS_TOKEN, TRACEREPORTS_RUN_NAME (default: la carpeta
 * del proyecto), TRACEREPORTS_ENV, TRACEREPORTS_PROJECT, TRACEREPORTS_RUN_ID, TRACEREPORTS_DISABLED.
 */
public class TraceReportsExtension implements BeforeEachCallback, AfterTestExecutionCallback, TestWatcher, ParameterResolver {
    private static final ExtensionContext.Namespace NS = ExtensionContext.Namespace.create(TraceReportsExtension.class);
    private static final String TEST = "tracereports-test";

    /** La ejecución compartida de la JVM: se cierra cuando JUnit termina (CloseableResource). */
    static final class Session implements ExtensionContext.Store.CloseableResource {
        final TraceReports cr = new TraceReports();

        Session() {
            String name = runName();
            if (name.isBlank()) {
                Path dir = Path.of("").toAbsolutePath().getFileName();
                name = dir == null ? "JUnit" : dir.toString();
            }
            cr.startRun(new TraceReports.RunInfo(name).environment(tracereports.Env.get("TRACEREPORTS_ENV")).framework("junit5"));
        }

        @Override
        public void close() {
            cr.finishRun();
            if (cr.recording()) {
                System.out.println("TraceReports (sin servidor): evidencia en " + cr.offlineDir() + "; reporte: "
                        + (cr.offlineReport() != null ? cr.offlineReport() : "`tracereports report " + cr.offlineDir() + " -o reporte`")
                        + "; para subirla: `tracereports push " + cr.offlineDir() + "`");
            } else if (cr.runId() != 0) System.out.println("TraceReports: " + cr.reportUrl());
            Delivery d = cr.delivery();
            int lost = d.problems() - d.runNotClosed();
            if (lost > 0) System.err.println("TraceReports: atención: " + lost + " eventos de evidencia no llegaron al servidor " + d);
            if (d.runNotClosed() > 0) {
                System.err.println("TraceReports: atención: el servidor no confirmó el cierre de la ejecución #" + cr.runId() + ": quedó abierta");
            }
        }
    }

    private static String runName() {
        return tracereports.Env.get("TRACEREPORTS_RUN_NAME"); // del entorno o del .env del proyecto
    }

    /** El cliente de la ejecución en curso (por si un test lo necesita directamente). */
    public static TraceReports report(ExtensionContext context) {
        return session(context).cr;
    }

    private static Session session(ExtensionContext context) {
        return context.getRoot().getStore(NS).getOrComputeIfAbsent(Session.class, k -> new Session(), Session.class);
    }

    @Override
    public void beforeEach(ExtensionContext context) {
        Session s = session(context);
        String cls = context.getRequiredTestClass().getName();
        Method method = context.getRequiredTestMethod();
        // dentro de un @ParameterizedTest / @RepeatedTest cada invocación es un caso: su identidad es
        // la posición (#n) que le da JUnit, nunca el nombre visible (que puede repetirse o cambiar)
        String index = invocationIndex(context.getUniqueId());
        String key = key(cls, method, index);
        List<String> tags = new ArrayList<>(context.getTags());
        tags.add(context.getRequiredTestClass().getSimpleName());
        String display = context.getDisplayName();
        String name = index.isEmpty() ? display
                : context.getParent().map(ExtensionContext::getDisplayName).orElse(method.getName())
                        + (display.contains("[" + index + "]") ? " " + display : " [" + index + "] " + display);
        TraceTest t = s.cr.startTest(new TraceReports.TestInfo(name.replaceAll("\\(\\)$", ""))
                .key(key).suite(cls).params(index.isEmpty() ? "" : "#" + index + " " + display).category(String.join(", ", tags)));
        context.getStore(NS).put(TEST, t);
    }

    /**
     * Identidad técnica: {@code paquete.Clase#método(TipoA,TipoB)} (la firma separa los métodos
     * sobrecargados) y {@code [#n]} si es una invocación de un test parametrizado o repetido. Con
     * {@link TraceReportsKey} se fija a mano. No incluye los valores de los parámetros (pueden ser secretos)
     * ni el nombre visible (puede repetirse o cambiar). Si cambia el orden de los casos, cambia #n.
     */
    static String key(String cls, Method method, String index) {
        TraceReportsKey fixed = method.getAnnotation(TraceReportsKey.class);
        if (fixed != null) {
            String v = fixed.value();
            if (index.isEmpty()) return v.replace("{index}", "");
            return v.contains("{index}") ? v.replace("{index}", index) : v + "[#" + index + "]";
        }
        StringBuilder sig = new StringBuilder(cls).append('#').append(method.getName()).append('(');
        Class<?>[] types = method.getParameterTypes();
        for (int i = 0; i < types.length; i++) {
            if (i > 0) sig.append(',');
            sig.append(types[i].getSimpleName());
        }
        sig.append(')');
        return index.isEmpty() ? sig.toString() : sig + "[#" + index + "]";
    }

    /** "3" para ".../[test-template-invocation:#3]", "" si no es una invocación de plantilla. */
    static String invocationIndex(String uniqueId) {
        java.util.regex.Matcher m = java.util.regex.Pattern.compile("-invocation:#(\\d+)\\]$").matcher(uniqueId);
        return m.find() ? m.group(1) : "";
    }

    private static TraceTest current(ExtensionContext context) {
        return context.getStore(NS).get(TEST, TraceTest.class);
    }

    @Override
    public void afterTestExecution(ExtensionContext context) {
        TraceTest t = current(context);
        if (t != null) t.runFinishHooks(context.getExecutionException().isPresent());
    }

    @Override
    public void testSuccessful(ExtensionContext context) {
        TraceTest t = current(context);
        if (t != null) t.finish(Status.PASS);
    }

    @Override
    public void testFailed(ExtensionContext context, Throwable cause) {
        TraceTest t = current(context);
        if (t == null) return;
        t.fail(TraceTest.summary(cause));
        t.finish(cause);
    }

    @Override
    public void testAborted(ExtensionContext context, Throwable cause) {
        TraceTest t = current(context);
        if (t == null) return;
        t.skip(String.valueOf(cause.getMessage()));
        t.finish(Status.SKIP);
    }

    @Override
    public void testDisabled(ExtensionContext context, Optional<String> reason) {
        // un @Disabled no pasa por beforeEach: se registra aquí para que aparezca como omitido
        Session s = session(context);
        String cls = context.getRequiredTestClass().getName();
        TraceTest t = s.cr.startTest(new TraceReports.TestInfo(context.getDisplayName().replaceAll("\\(\\)$", ""))
                .key(key(cls, context.getRequiredTestMethod(), "")).suite(cls));
        reason.ifPresent(t::skip);
        t.finish(Status.SKIP);
    }

    @Override
    public boolean supportsParameter(ParameterContext p, ExtensionContext e) {
        return p.getParameter().getType() == TraceTest.class;
    }

    @Override
    public Object resolveParameter(ParameterContext p, ExtensionContext e) {
        TraceTest t = current(e);
        return t != null ? t : report(e).startTest("(sin test)");
    }
}
