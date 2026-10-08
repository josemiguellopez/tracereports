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

    /** Valor numérico de "key" en un JSON plano (p. ej. {"run_id": 7}; negativo: id local sin servidor), o 0. */
    static long number(String json, String key) {
        if (json == null) return 0;
        Matcher m = Pattern.compile("\"" + Pattern.quote(key) + "\"\\s*:\\s*(-?\\d+)").matcher(json);
        return m.find() ? Long.parseLong(m.group(1)) : 0;
    }

    /** Lee los metadatos compartidos de una grabación sin perder precisión en los ids. */
    static Object read(String text) {
        Reader reader = new Reader(text);
        Object value = reader.value(0);
        reader.space();
        if (reader.pos != text.length()) throw new IllegalArgumentException("JSON trailing data");
        return value;
    }

    private static final class Reader {
        final String text;
        int pos;
        Reader(String text) { this.text = text; }
        void space() { while (pos < text.length() && " \t\r\n".indexOf(text.charAt(pos)) >= 0) pos++; }
        boolean take(char c) { space(); if (pos < text.length() && text.charAt(pos) == c) { pos++; return true; } return false; }
        void need(char c) { if (!take(c)) throw new IllegalArgumentException("Invalid JSON at " + pos); }
        Object value(int depth) {
            space();
            if (depth > 100 || pos >= text.length()) throw new IllegalArgumentException("Invalid JSON");
            char c = text.charAt(pos);
            if (c == '"') return string();
            if (take('{')) {
                Map<String, Object> out = new java.util.LinkedHashMap<>();
                if (take('}')) return out;
                do { space(); String key = string(); need(':'); out.put(key, value(depth + 1)); } while (take(','));
                need('}'); return out;
            }
            if (take('[')) {
                java.util.List<Object> out = new java.util.ArrayList<>();
                if (take(']')) return out;
                do { out.add(value(depth + 1)); } while (take(','));
                need(']'); return out;
            }
            for (String literal : new String[]{"true", "false", "null"}) {
                if (text.startsWith(literal, pos)) {
                    pos += literal.length();
                    return literal.equals("null") ? null : Boolean.valueOf(literal);
                }
            }
            Matcher m = Pattern.compile("-?(?:0|[1-9][0-9]*)(?:\\.[0-9]+)?(?:[eE][+-]?[0-9]+)?").matcher(text);
            m.region(pos, text.length());
            if (!m.lookingAt()) throw new IllegalArgumentException("Invalid JSON number");
            String n = m.group(); pos = m.end();
            if (n.indexOf('.') >= 0 || n.indexOf('e') >= 0 || n.indexOf('E') >= 0) return Double.valueOf(n);
            return Long.valueOf(n);
        }
        String string() {
            need('"');
            StringBuilder out = new StringBuilder();
            while (pos < text.length()) {
                char c = text.charAt(pos++);
                if (c == '"') return out.toString();
                if (c < 32) throw new IllegalArgumentException("Invalid JSON string");
                if (c == '\\') {
                    if (pos == text.length()) break;
                    c = text.charAt(pos++);
                    switch (c) {
                        case '"', '\\', '/' -> out.append(c);
                        case 'b' -> out.append('\b');
                        case 'f' -> out.append('\f');
                        case 'n' -> out.append('\n');
                        case 'r' -> out.append('\r');
                        case 't' -> out.append('\t');
                        case 'u' -> {
                            if (pos + 4 > text.length()) throw new IllegalArgumentException("Invalid JSON escape");
                            out.append((char) Integer.parseInt(text.substring(pos, pos + 4), 16)); pos += 4;
                        }
                        default -> throw new IllegalArgumentException("Invalid JSON escape");
                    }
                } else out.append(c);
            }
            throw new IllegalArgumentException("Unterminated JSON string");
        }
    }
}
