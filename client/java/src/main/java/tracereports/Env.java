package tracereports;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

/**
 * Variables del cliente (TRACEREPORTS_X): primero del entorno; si no está, del archivo .env del
 * proyecto (el mismo que lee el servidor), así el token vive en un solo lugar, ignorado por git, sin
 * definirlo en cada terminal ni de forma global. Las variables del entorno siempre ganan (en CI
 * llegan como secrets). Del archivo solo se usan las claves TRACEREPORTS_*.
 *
 * <p>El .env se busca desde la carpeta actual hacia arriba, hasta la raíz del repositorio (la
 * carpeta con .git). TRACEREPORTS_ENV_FILE indica otro archivo, u "off" para no leer ninguno.
 */
public final class Env {
    private static final String PREFIX = "TRACEREPORTS_";

    private static Map<String, String> fileValues;
    private static Path filePath;

    private Env() {}

    /** TRACEREPORTS_X: del entorno y, si no está, del .env del proyecto. "" si no hay. */
    public static String get(String key) {
        String v = System.getenv(key);
        if (v == null || v.isBlank()) v = file().get(key);
        return v == null || v.isBlank() ? "" : v.trim();
    }

    /** Ruta del .env del que se leyó alguna variable (para los mensajes), o null. */
    public static synchronized Path fileInUse() {
        return file().isEmpty() ? null : filePath;
    }

    /** Vuelve a buscar el .env en la próxima lectura (tests). */
    static synchronized void reset() {
        fileValues = null;
        filePath = null;
    }

    private static synchronized Map<String, String> file() {
        if (fileValues == null) {
            filePath = find(Paths.get("").toAbsolutePath(), System.getenv(PREFIX + "ENV_FILE"));
            Map<String, String> out = new LinkedHashMap<>();
            if (filePath != null) {
                parse(filePath).forEach((k, v) -> {
                    if (k.startsWith(PREFIX)) out.put(k, v);
                });
            }
            fileValues = Collections.unmodifiableMap(out);
        }
        return fileValues;
    }

    /** El .env que corresponde desde start, o null si no hay o está desactivado ("off"). */
    static Path find(Path start, String explicit) {
        if (explicit != null && !explicit.isBlank()) {
            return explicit.trim().equalsIgnoreCase("off") ? null : Paths.get(explicit.trim());
        }
        for (Path d = start; d != null; d = d.getParent()) {
            Path candidate = d.resolve(".env");
            if (Files.isRegularFile(candidate)) return candidate;
            if (Files.exists(d.resolve(".git"))) return null;
        }
        return null;
    }

    /** Líneas KEY=VALUE con las mismas reglas que el servidor. */
    static Map<String, String> parse(Path file) {
        Map<String, String> values = new LinkedHashMap<>();
        List<String> lines;
        try {
            lines = Files.readAllLines(file, StandardCharsets.UTF_8);
        } catch (IOException e) {
            return values;
        }
        for (String raw : lines) {
            String line = raw.replace("﻿", "").trim();
            if (line.isEmpty() || line.startsWith("#")) continue;
            if (line.startsWith("export ")) line = line.substring("export ".length());
            int eq = line.indexOf('=');
            if (eq < 0) continue;
            String key = line.substring(0, eq).trim();
            if (key.isEmpty() || key.contains(" ") || key.contains("\t")) continue;
            String val = line.substring(eq + 1).trim();
            if (val.length() >= 2 && val.charAt(0) == val.charAt(val.length() - 1)
                    && (val.charAt(0) == '"' || val.charAt(0) == '\'')) {
                val = val.substring(1, val.length() - 1);
            } else if (val.contains(" #")) {
                val = val.substring(0, val.indexOf(" #")).trim();
            }
            values.put(key, val);
        }
        return values;
    }
}
