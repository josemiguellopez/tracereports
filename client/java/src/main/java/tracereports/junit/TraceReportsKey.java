package tracereports.junit;

import java.lang.annotation.Documented;
import java.lang.annotation.ElementType;
import java.lang.annotation.Retention;
import java.lang.annotation.RetentionPolicy;
import java.lang.annotation.Target;

/**
 * Identidad estable de un test en TraceReports, en lugar de la automática
 * ({@code paquete.Clase#método(Tipos)} y, en un test parametrizado o repetido, {@code [#n]}).
 *
 * <p>Úsala si renombras el método o la clase y quieres conservar el historial. En un test
 * parametrizado se le agrega {@code [#n]} (posición del caso), salvo que el valor ya contenga
 * {@code {index}}. No pongas valores secretos: la identidad se guarda y se muestra.
 *
 * <pre>{@code
 * @TraceReportsKey("login-admin")
 * @Test void loginCorrecto() { ... }
 * }</pre>
 */
@Documented
@Retention(RetentionPolicy.RUNTIME)
@Target(ElementType.METHOD)
public @interface TraceReportsKey {
    String value();
}
