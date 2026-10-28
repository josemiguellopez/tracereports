package tracereports;

import java.util.ArrayList;
import java.util.List;
import java.util.Locale;
import java.util.function.UnaryOperator;

/**
 * Capturas sin datos sensibles: se tapan antes de capturar.
 *
 * <ul>
 *   <li>{@code TRACEREPORTS_SCREENSHOT_MASK}: selectores CSS a tapar (un grupo CSS: {@code "input[name=rut], .tarjeta"}).</li>
 *   <li>Siempre se tapan {@code input[type=password]} y los elementos con el atributo {@code data-tracereports-mask};
 *       {@code TRACEREPORTS_SCREENSHOT_MASK_DEFAULTS=off} lo desactiva.</li>
 *   <li>{@code TRACEREPORTS_SCREENSHOTS}: {@code all} (por defecto), {@code failures} (solo la captura de un paso FAIL)
 *       u {@code off} (ninguna). El servidor aplica la misma política por su cuenta.</li>
 * </ul>
 *
 * Playwright usa su opción nativa {@code mask} ({@code PlaywrightEvidence.screenshot}). Selenium no la tiene: se
 * ponen recuadros del mismo color sobre los elementos, se captura y se quitan, también si la captura falla
 * ({@code SeleniumEvidence.screenshot}). Esta clase no depende de ninguno de los dos.
 */
public final class Mask {
    private Mask() {}

    /** Lo que se tapa siempre (salvo TRACEREPORTS_SCREENSHOT_MASK_DEFAULTS=off). */
    public static final String DEFAULT_MASK = "input[type=password], [data-tracereports-mask]";
    /** Color de los recuadros: el mismo que usa Playwright por defecto. */
    public static final String COLOR = "#FF00FF";
    /** Atributo de los recuadros que pone Selenium (para quitarlos todos). */
    public static final String OVERLAY_ATTR = "data-tracereports-mask-overlay";

    /** Recuadros fijos sobre cada elemento visible del selector; argumentos: selector, color, atributo. */
    public static final String COVER_JS = String.join("\n",
            "const [sel, color, attr] = arguments;",
            "let n = 0;",
            "for (const el of document.querySelectorAll(sel)) {",
            "  const r = el.getBoundingClientRect();",
            "  if (!r.width || !r.height) continue;",
            "  const d = document.createElement('div');",
            "  d.setAttribute(attr, '');",
            "  d.style.cssText = 'position:fixed;left:' + r.left + 'px;top:' + r.top + 'px;width:' + r.width + 'px;height:' + r.height + 'px;' +",
            "    'background:' + color + ';z-index:2147483647;pointer-events:none;margin:0;padding:0;border:0';",
            "  document.documentElement.appendChild(d);",
            "  n++;",
            "}",
            "return n;");

    /** Quita los recuadros; argumento: el atributo. */
    public static final String UNCOVER_JS = "for (const d of document.querySelectorAll('[' + arguments[0] + ']')) d.remove();";

    /** El grupo de selectores CSS a tapar ("" = nada). */
    public static String selector() {
        return selector(Env::get);
    }

    static String selector(UnaryOperator<String> env) {
        List<String> parts = new ArrayList<>();
        String defaults = nonNull(env.apply("TRACEREPORTS_SCREENSHOT_MASK_DEFAULTS")).trim().toLowerCase(Locale.ROOT);
        if (!List.of("off", "0", "false", "no").contains(defaults)) parts.add(DEFAULT_MASK);
        String extra = nonNull(env.apply("TRACEREPORTS_SCREENSHOT_MASK")).trim().replaceAll("^,+|,+$", "").trim();
        if (!extra.isEmpty()) parts.add(extra);
        return String.join(", ", parts);
    }

    /** TRACEREPORTS_SCREENSHOTS: all, failures u off (un valor desconocido cuenta como all). */
    public static String policy() {
        return policy(Env::get);
    }

    static String policy(UnaryOperator<String> env) {
        String v = nonNull(env.apply("TRACEREPORTS_SCREENSHOTS")).trim().toLowerCase(Locale.ROOT);
        return v.equals("failures") || v.equals("off") ? v : "all";
    }

    /** Si la política deja enviar una captura de un paso con este estado. */
    public static boolean allowed(Status status) {
        return allowed(policy(), status);
    }

    static boolean allowed(String policy, Status status) {
        if (policy.equals("off")) return false;
        return policy.equals("all") || status == Status.FAIL;
    }

    private static String nonNull(String s) {
        return s == null ? "" : s;
    }
}
