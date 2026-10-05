package tracereports;

/**
 * Qué pasó con la evidencia: enviados, reintentos, rechazados por el servidor (4xx), descartados
 * por cola llena, perdidos al cerrar, pendientes, tests que no se pudieron registrar y
 * {@code runNotClosed} (1 si el servidor no confirmó el cierre de la ejecución: quedó abierta).
 */
public record Delivery(int sent, int retried, int rejected, int dropped, int lost, int pending, int unregisteredTests,
                       int runNotClosed) {
    /** Lo que no llegó al servidor, incluido un cierre de ejecución no confirmado. */
    public int problems() {
        return rejected + dropped + lost + pending + unregisteredTests + runNotClosed;
    }
}
