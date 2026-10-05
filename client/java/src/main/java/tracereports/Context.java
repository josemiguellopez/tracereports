package tracereports;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.TimeUnit;

/**
 * Contexto de la ejecución: rama y commit, de las variables de los CI más comunes o de git. El
 * servidor solo compara una ejecución con otras del mismo proyecto, ambiente y rama.
 */
final class Context {
    private static final String[] BRANCH_VARS = {"TRACEREPORTS_BRANCH", "GITHUB_HEAD_REF", "GITHUB_REF_NAME", "CI_COMMIT_REF_NAME",
            "BITBUCKET_BRANCH", "BUILD_SOURCEBRANCHNAME", "BRANCH_NAME", "CIRCLE_BRANCH", "GIT_BRANCH"};
    private static final String[] COMMIT_VARS = {"TRACEREPORTS_COMMIT", "GITHUB_SHA", "CI_COMMIT_SHA", "BITBUCKET_COMMIT",
            "BUILD_SOURCEVERSION", "CIRCLE_SHA1", "GIT_COMMIT"};

    private Context() {}

    static String branch() {
        String b = firstEnv(BRANCH_VARS);
        if (b.isEmpty()) b = git("rev-parse", "--abbrev-ref", "HEAD");
        return "HEAD".equals(b) ? "" : b;
    }

    static String commit() {
        String c = firstEnv(COMMIT_VARS);
        return c.isEmpty() ? git("rev-parse", "HEAD") : c;
    }

    /** TRACEREPORTS_X, del entorno o del .env del proyecto (ver {@link Env}). */
    static String env(String key) {
        return Env.get(key);
    }

    /** Propiedad de sistema tracereports.X, o el valor por defecto. */
    static String property(String name, String fallback) {
        String v = System.getProperty("tracereports." + name);
        return v == null || v.isBlank() ? fallback : v;
    }

    private static String firstEnv(String[] keys) {
        for (String k : keys) {
            String v = env(k);
            if (!v.isEmpty()) return v.startsWith("origin/") ? v.substring(7) : v;
        }
        return "";
    }

    private static String git(String... args) {
        String[] cmd = new String[args.length + 1];
        cmd[0] = "git";
        System.arraycopy(args, 0, cmd, 1, args.length);
        try {
            Process p = new ProcessBuilder(cmd).redirectErrorStream(false).start();
            if (!p.waitFor(2, TimeUnit.SECONDS)) {
                p.destroyForcibly();
                return "";
            }
            return p.exitValue() == 0 ? new String(p.getInputStream().readAllBytes(), StandardCharsets.UTF_8).trim() : "";
        } catch (IOException e) {
            return "";
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            return "";
        }
    }
}
