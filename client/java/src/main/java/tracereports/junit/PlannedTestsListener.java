package tracereports.junit;

import java.util.concurrent.atomic.AtomicInteger;
import org.junit.platform.engine.TestExecutionResult;
import org.junit.platform.launcher.TestExecutionListener;
import org.junit.platform.launcher.TestIdentifier;
import org.junit.platform.launcher.TestPlan;

/**
 * Cuenta los tests del plan de JUnit antes de que corra el primero: son los planificados de la
 * ejecución (progreso y ETA en En vivo). Se registra solo (META-INF/services) y no hace nada más.
 */
public class PlannedTestsListener implements TestExecutionListener {
    private static final AtomicInteger PLANNED = new AtomicInteger();

    /** Los tests del plan en curso (0 si JUnit no lo informó). */
    public static int planned() {
        return PLANNED.get();
    }

    @Override
    public void testPlanExecutionStarted(TestPlan testPlan) {
        PLANNED.set((int) Math.min(Integer.MAX_VALUE, testPlan.countTestIdentifiers(TestIdentifier::isTest)));
    }

    @Override
    public void executionFinished(TestIdentifier id, TestExecutionResult result) {
        // nada: solo interesa el total al empezar
    }
}
