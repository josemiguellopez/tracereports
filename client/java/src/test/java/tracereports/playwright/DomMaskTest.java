package tracereports.playwright;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;
import tracereports.Dom;

/**
 * El snapshot del DOM no lleva lo tapado: ni el texto del elemento enmascarado ni el de un contenedor
 * (una ficha con id) que lo tiene adentro. Playwright y el script de Selenium, en un Chromium real.
 */
class DomMaskTest {
    static final String FICHA = "<section id='ficha'><h2>Atención</h2><p>Paciente: <span data-tracereports-mask>Juana Pérez Soto</span></p>"
            + "<label for='pw'>Clave</label><input id='pw' type='password' value='secreta'><button id='guardar'>Guardar</button></section>";

    @Test
    void theSnapshotNeverCarriesMaskedText() throws Exception {
        MaskTest.withPage(page -> {
            page.setContent(FICHA);
            String mask = "input[type=password], [data-tracereports-mask]";
            Object viaPlaywright = page.evaluate(Dom.FUNCTION, mask);
            // Selenium: el mismo script como texto, con la máscara en arguments[0]
            Object viaSelenium = page.evaluate("([script, m]) => new Function(script).call(null, m)", List.of(Dom.SCRIPT, mask));
            for (Object snap : List.of(viaPlaywright, viaSelenium)) {
                String raw = String.valueOf(snap);
                assertFalse(raw.contains("Juana") || raw.contains("secreta"), raw);
                @SuppressWarnings("unchecked")
                List<Map<String, Object>> els = (List<Map<String, Object>>) ((Map<String, Object>) snap).get("elements");
                String ficha = els.stream().filter(e -> "ficha".equals(e.get("id"))).findFirst().orElseThrow().get("text").toString();
                assertTrue(ficha.matches("Atención Paciente: ••• Clave ••• Guardar"), ficha);
                assertEquals("Guardar", els.stream().filter(e -> "guardar".equals(e.get("id"))).findFirst().orElseThrow().get("text"));
            }
        });
    }
}
