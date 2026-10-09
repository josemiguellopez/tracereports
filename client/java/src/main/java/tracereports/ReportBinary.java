package tracereports;

import java.io.*;
import java.net.URI;
import java.net.http.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.security.MessageDigest;
import java.time.Duration;
import java.util.*;
import java.util.concurrent.*;
import java.nio.ByteBuffer;
import java.util.zip.*;

/** Verified, pinned renderer cached in the same location as the other clients. */
final class ReportBinary {
    static final String VERSION = "0.2.0";
    private static final String RELEASES = "https://github.com/josemiguellopez/tracereports/releases/download";
    private static final int MAX_BINARY = 256 * 1024 * 1024;

    static String download() throws Exception {
        String os = System.getProperty("os.name").toLowerCase(Locale.ROOT);
        os = os.startsWith("windows") ? "windows" : os.contains("mac") ? "darwin" : os.equals("linux") ? "linux" : "";
        String arch = System.getProperty("os.arch").toLowerCase(Locale.ROOT);
        arch = Set.of("amd64", "x86_64").contains(arch) ? "amd64" : Set.of("aarch64", "arm64").contains(arch) ? "arm64" : "";
        if (os.isEmpty() || arch.isEmpty()) throw new IOException("unsupported report platform");
        String base = System.getenv(os.equals("windows") ? "LOCALAPPDATA" : os.equals("darwin") ? "" : "XDG_CACHE_HOME");
        Path root = base == null || base.isBlank()
                ? Path.of(System.getProperty("user.home"), os.equals("darwin") ? "Library/Caches" : ".cache") : Path.of(base);
        Path parent = root.resolve("tracereports").resolve(VERSION), target = parent.resolve(os + "_" + arch);
        String name = os.equals("windows") ? "tracereports.exe" : "tracereports";
        Path binary = target.resolve(name), lock = parent.resolve(os + "_" + arch + ".lock");
        if (cached(binary)) return binary.toString();
        if (Context.property("binDownload", Context.env("TRACEREPORTS_BIN_DOWNLOAD")).equals("0")) throw new IOException("binary download disabled");
        long deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(60);
        Files.createDirectories(parent);
        while (true) {
            if (cached(binary)) return binary.toString();
            remaining(deadline);
            try { Files.createDirectory(lock); break; }
            catch (FileAlreadyExistsException e) { Thread.sleep(50); }
        }
        Path staging = null;
        try {
            if (cached(binary)) return binary.toString();
            String baseUrl = Context.property("binBaseUrl", Context.env("TRACEREPORTS_BIN_BASE_URL"));
            if (baseUrl.isBlank()) baseUrl = RELEASES;
            if (!baseUrl.equals(RELEASES)) {
                URI u = URI.create(baseUrl);
                if (!"http".equals(u.getScheme()) || !Set.of("localhost", "127.0.0.1", "::1").contains(u.getHost()) || u.getUserInfo() != null)
                    throw new IOException("BIN_BASE_URL must be a loopback test server");
            }
            String asset = "tracereports_" + VERSION + "_" + os + "_" + arch + (os.equals("windows") ? ".zip" : ".tar.gz");
            String prefix = baseUrl.replaceAll("/+$", "") + "/v" + VERSION + "/";
            HttpClient http = HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(10)).followRedirects(HttpClient.Redirect.NORMAL).build();
            String checksums = new String(fetch(http, prefix + "checksums.txt", 1024 * 1024, deadline), StandardCharsets.UTF_8);
            String expected = null;
            for (String line : checksums.split("\\R")) {
                String[] parts = line.trim().split("\\s+");
                if (parts.length == 2 && parts[1].replaceFirst("^\\*", "").equals(asset)) {
                    if (expected != null) throw new IOException("ambiguous release checksum");
                    expected = parts[0].toLowerCase(Locale.ROOT);
                }
            }
            if (expected == null || !expected.matches("[0-9a-f]{64}")) throw new IOException("release checksum missing");
            byte[] archive = fetch(http, prefix + asset, 128 * 1024 * 1024, deadline);
            if (!sha(archive).equals(expected)) throw new IOException("release checksum mismatch");
            byte[] data = executable(archive, name, os.equals("windows"));
            Files.createDirectories(target);
            staging = Files.createTempDirectory(parent, ".download-");
            Path staged = staging.resolve(name);
            Files.write(staged, data);
            if (!os.equals("windows") && !staged.toFile().setExecutable(true, true)) throw new IOException("cannot make renderer executable");
            Files.writeString(staging.resolve("binary.sha256"), sha(data), StandardCharsets.US_ASCII);
            Files.deleteIfExists(binary);
            Files.deleteIfExists(target.resolve("binary.sha256"));
            Files.move(staging.resolve("binary.sha256"), target.resolve("binary.sha256"), StandardCopyOption.REPLACE_EXISTING);
            Files.move(staged, binary, StandardCopyOption.REPLACE_EXISTING);
            return binary.toString();
        } finally {
            try {
                if (staging != null) {
                    Files.deleteIfExists(staging.resolve(name));
                    Files.deleteIfExists(staging.resolve("binary.sha256"));
                    Files.deleteIfExists(staging);
                }
            } finally { Files.deleteIfExists(lock); }
        }
    }

    private static boolean cached(Path binary) {
        try { return Files.isRegularFile(binary, LinkOption.NOFOLLOW_LINKS) && sha(Files.readAllBytes(binary)).equals(Files.readString(binary.resolveSibling("binary.sha256")).trim()); }
        catch (Exception e) { return false; }
    }
    private static String sha(byte[] data) throws Exception { return HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(data)); }
    private static long remaining(long deadline) throws TimeoutException {
        long n = deadline - System.nanoTime();
        if (n <= 0) throw new TimeoutException("report binary download timed out");
        return n;
    }
    private static byte[] fetch(HttpClient http, String url, int limit, long deadline) throws Exception {
        HttpRequest request = HttpRequest.newBuilder(URI.create(url)).timeout(Duration.ofNanos(remaining(deadline))).GET().build();
        // The future includes the body, so a stalled body is bounded by the same deadline.
        var future = http.sendAsync(request, info -> new HttpResponse.BodySubscriber<byte[]>() {
            final HttpResponse.BodySubscriber<byte[]> body = HttpResponse.BodySubscribers.ofByteArray();
            Flow.Subscription subscription;
            long size;
            public CompletionStage<byte[]> getBody() { return body.getBody(); }
            public void onSubscribe(Flow.Subscription s) { subscription = s; body.onSubscribe(s); }
            public void onNext(List<ByteBuffer> buffers) {
                for (ByteBuffer buffer : buffers) size += buffer.remaining();
                if (size > limit) { subscription.cancel(); body.onError(new IOException("release download too large")); }
                else body.onNext(buffers);
            }
            public void onError(Throwable error) { body.onError(error); }
            public void onComplete() { body.onComplete(); }
        });
        try {
            HttpResponse<byte[]> response = future.get(remaining(deadline), TimeUnit.NANOSECONDS);
            if (response.statusCode() != 200) throw new IOException("release download HTTP " + response.statusCode());
            return response.body();
        } finally { future.cancel(true); }
    }
    // Only the exact executable member is copied; archive names never become output paths.
    private static byte[] executable(byte[] archive, String name, boolean zipped) throws IOException {
        byte[] found = null;
        if (zipped) {
            try (ZipInputStream zip = new ZipInputStream(new ByteArrayInputStream(archive))) {
                for (ZipEntry e; (e = zip.getNextEntry()) != null; ) {
                    if (!e.getName().equals(name) || e.isDirectory()) continue;
                    if (found != null) throw new IOException("duplicate release executable");
                    found = zip.readNBytes(MAX_BINARY + 1);
                    if (found.length > MAX_BINARY) throw new IOException("release executable too large");
                }
            }
        } else {
            try (GZIPInputStream gzip = new GZIPInputStream(new ByteArrayInputStream(archive))) {
                long total = 0;
                while (true) {
                    byte[] header = gzip.readNBytes(512);
                    if (header.length == 0 || header[0] == 0) break;
                    if (header.length != 512) throw new IOException("truncated TAR");
                    String member = new String(header, 0, 100, StandardCharsets.UTF_8).split("\0", 2)[0];
                    long size = Long.parseLong(new String(header, 124, 12, StandardCharsets.US_ASCII).replace("\0", "").trim(), 8);
                    total += 512 + ((size + 511) / 512) * 512;
                    if (size < 0 || total > MAX_BINARY + 8 * 1024 * 1024) throw new IOException("release archive too large");
                    if (member.equals(name) && (header[156] == 0 || header[156] == '0') && header[345] == 0) {
                        if (found != null || size > MAX_BINARY) throw new IOException("invalid release executable");
                        found = gzip.readNBytes((int) size);
                        if (found.length != size) throw new IOException("truncated release executable");
                    } else { gzip.skipNBytes(size); }
                    gzip.skipNBytes((512 - size % 512) % 512);
                }
            }
        }
        if (found == null || found.length == 0) throw new IOException("release executable missing");
        return found;
    }
}
