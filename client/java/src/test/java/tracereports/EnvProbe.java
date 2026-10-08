package tracereports;

/** Proceso aparte para EnvTest (el entorno de un proceso no se cambia desde Java): imprime Env.get. */
public final class EnvProbe {
    private EnvProbe() {}

    public static void main(String[] args) {
        System.out.print("[" + Env.get(args[0]) + "]");
    }
}
