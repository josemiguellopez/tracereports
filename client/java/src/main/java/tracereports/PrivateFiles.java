package tracereports;

import java.io.IOException;
import java.io.OutputStream;
import java.nio.ByteBuffer;
import java.nio.channels.Channels;
import java.nio.channels.SeekableByteChannel;
import java.nio.charset.StandardCharsets;
import java.nio.file.FileSystems;
import java.nio.file.Files;
import java.nio.file.OpenOption;
import java.nio.file.Path;
import java.nio.file.StandardOpenOption;
import java.nio.file.attribute.FileAttribute;
import java.nio.file.attribute.PosixFilePermissions;
import java.util.Set;

/**
 * Archivos de la grabación offline: llevan la evidencia sin redactar (eso lo hace el servidor), así
 * que solo los lee el usuario (carpetas 0700, archivos 0600). Donde no hay permisos POSIX (Windows)
 * se crean como siempre.
 */
final class PrivateFiles {
    private static final boolean POSIX = FileSystems.getDefault().supportedFileAttributeViews().contains("posix");

    private PrivateFiles() {}

    private static FileAttribute<?>[] attrs(String perms) {
        return POSIX ? new FileAttribute<?>[] {PosixFilePermissions.asFileAttribute(PosixFilePermissions.fromString(perms))}
                : new FileAttribute<?>[0];
    }

    /** Crea la carpeta y las que falten (0700); una que ya existía no se toca. */
    static Path dirs(Path dir) throws IOException {
        return Files.createDirectories(dir, attrs("rwx------"));
    }

    /** Crea una carpeta (0700); falla si ya existe (sirve de lock). */
    static Path dir(Path dir) throws IOException {
        return Files.createDirectory(dir, attrs("rwx------"));
    }

    /** Escribe un archivo (0600 si lo crea). Sin opciones: crea o reemplaza. */
    static void write(Path file, byte[] data, OpenOption... options) throws IOException {
        Set<OpenOption> opts = options.length == 0
                ? Set.of(StandardOpenOption.CREATE, StandardOpenOption.TRUNCATE_EXISTING, StandardOpenOption.WRITE)
                : Set.of(options);
        if (!opts.contains(StandardOpenOption.WRITE) && !opts.contains(StandardOpenOption.APPEND)) {
            opts = new java.util.HashSet<>(opts);
            opts.add(StandardOpenOption.WRITE);
        }
        try (SeekableByteChannel ch = Files.newByteChannel(file, opts, attrs("rw-------"))) {
            ByteBuffer buf = ByteBuffer.wrap(data);
            while (buf.hasRemaining()) ch.write(buf);
        }
    }

    static void writeString(Path file, String text, OpenOption... options) throws IOException {
        write(file, text.getBytes(StandardCharsets.UTF_8), options);
    }

    /** Abre un archivo para agregar al final (0600 si lo crea). */
    static OutputStream append(Path file) throws IOException {
        return Channels.newOutputStream(Files.newByteChannel(file,
                Set.of(StandardOpenOption.CREATE, StandardOpenOption.APPEND, StandardOpenOption.WRITE), attrs("rw-------")));
    }
}
