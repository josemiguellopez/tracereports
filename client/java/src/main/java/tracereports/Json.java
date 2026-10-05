package tracereports;

import java.util.Collection;
import java.util.Map;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/** JSON mínimo (sin dependencias): escribe Map/List/String/Number/Boolean y lee números de una respuesta. */
final class Json {
    private Json() {}

    static String write(Object v) {
        StringBuilder b = new StringBuilder();
        write(b, v);
        return b.toString();
    }

    private static void write(StringBuilder b, Object v) {
        if (v == null) {
            b.append("null");
        } else if (v instanceof String s) {
            quote(b, s);
        } else if (v instanceof Number || v instanceof Boolean) {
            b.append(v);
        } else if (v instanceof Map<?, ?> m) {
            b.append('{');
            boolean first = true;
            for (Map.Entry<?, ?> e : m.entrySet()) {
                if (!first) b.append(',');
                first = false;
                quote(b, String.valueOf(e.getKey()));
                b.append(':');
                write(b, e.getValue());
            }
            b.append('}');
        } else if (v instanceof Collection<?> c) {
            b.append('[');
            boolean first = true;
            for (Object o : c) {
                if (!first) b.append(',');
                first = false;
                write(b, o);
            }
            b.append(']');
        } else if (v instanceof Object[] arr) {
            write(b, java.util.Arrays.asList(arr));
        } else {
            quote(b, v.toString());
        }
    }

    private static void quote(StringBuilder b, String s) {
        b.append('"');
        for (int i = 0; i < s.length(); i++) {
            char c = s.charAt(i);
            switch (c) {
                case '"' -> b.append("\\\"");
                case '\\' -> b.append("\\\\");
                case '\n' -> b.append("\\n");
                case '\r' -> b.append("\\r");
                case '\t' -> b.append("\\t");
                default -> {
                    if (c < 0x20) b.append(String.format("\\u%04x", (int) c));
                    else b.append(c);
                }
            }
        }
        b.append('"');
    }

    /** Valor numérico de "key" en un JSON plano (p. ej. {"run_id": 7}), o 0. */
    static long number(String json, String key) {
        if (json == null) return 0;
        Matcher m = Pattern.compile("\"" + Pattern.quote(key) + "\"\\s*:\\s*(\\d+)").matcher(json);
        return m.find() ? Long.parseLong(m.group(1)) : 0;
    }
}
