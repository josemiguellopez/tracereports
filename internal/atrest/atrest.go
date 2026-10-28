// Package atrest encrypts the evidence at rest (TRACEREPORTS_ENCRYPT_AT_REST=1, opt-in): the files
// of the screenshots folder (screenshots, Playwright traces and videos) and the DOM snapshots kept in
// the database. It is plan B of the #41 spike: an encrypted SQLite in pure Go cost more than the
// agreed 30 %, so the database stays as it is (on an encrypted volume) and the evidence that shows
// the most (what was on the screen) is encrypted by the application, whatever the host does.
//
// Keys: one master key of 32 random bytes (TRACEREPORTS_DB_KEY or TRACEREPORTS_DB_KEY_FILE, never
// inside DATA_DIR) and, while rotating, the previous one (*_PREVIOUS) to read what it encrypted. Each
// use gets its own subkey (HKDF-SHA256): files and DOM never share a key.
//
// File format (version 1), a STREAM construction over AES-256-GCM so a video is never loaded whole:
//
//	"TRENC" | version (1) | key id (8) | nonce prefix (7) | chunk 0 | chunk 1 | ...
//
// Each chunk is up to 64 KiB of plaintext sealed with nonce = prefix | counter (4, big endian) |
// last (1: 1 on the final chunk) and the header as additional data: chunks cannot be reordered,
// dropped, appended or moved to another file, and the key id says which key opens the file.
package atrest

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	magic      = "TRENC"
	version    = 1
	headerLen  = len(magic) + 1 + 8 + 7
	chunkPlain = 64 << 10
	tagLen     = 16

	purposeFiles = "tracereports/files/v1"
	purposeDOM   = "tracereports/dom/v1"

	// sealPrefix marks a sealed text value (the DOM): "atrest:v1:<key id>:<base64(nonce||ct)>".
	sealPrefix = "atrest:v1:"
)

var (
	// ErrNoKey: an encrypted file or value and no key that opens it.
	ErrNoKey = errors.New("encrypted with a key that is not configured (TRACEREPORTS_DB_KEY / TRACEREPORTS_DB_KEY_PREVIOUS)")
	// ErrCorrupt: not valid ciphertext, or it was altered.
	ErrCorrupt = errors.New("the encrypted evidence is damaged or was altered")
)

type keySet struct {
	id    string // primeros 8 bytes de SHA-256 de la clave maestra, en hex
	files cipher.AEAD
	dom   cipher.AEAD
}

func newKeySet(master []byte) (*keySet, error) {
	if len(master) != 32 {
		return nil, errors.New("the key must be 32 bytes")
	}
	sum := sha256.Sum256(master)
	ks := &keySet{id: hex.EncodeToString(sum[:8])}
	for _, p := range []struct {
		purpose string
		dst     *cipher.AEAD
	}{{purposeFiles, &ks.files}, {purposeDOM, &ks.dom}} {
		sub, err := hkdf.Key(sha256.New, master, nil, p.purpose, 32)
		if err != nil {
			return nil, err
		}
		block, err := aes.NewCipher(sub)
		if err != nil {
			return nil, err
		}
		if *p.dst, err = cipher.NewGCM(block); err != nil {
			return nil, err
		}
	}
	return ks, nil
}

// Vault encrypts and decrypts the evidence. A nil Vault encrypts nothing and only reads plain
// evidence; with Encrypt false (keys given only to read old evidence) it writes in clear.
type Vault struct {
	current  *keySet
	previous *keySet
	// EncryptNew: new evidence is written encrypted (TRACEREPORTS_ENCRYPT_AT_REST=1).
	EncryptNew bool
}

// New builds a Vault from raw keys (previous may be nil).
func New(current, previous []byte, encrypt bool) (*Vault, error) {
	v := &Vault{EncryptNew: encrypt}
	var err error
	if v.current, err = newKeySet(current); err != nil {
		return nil, err
	}
	if previous != nil {
		if v.previous, err = newKeySet(previous); err != nil {
			return nil, fmt.Errorf("previous key: %w", err)
		}
	}
	return v, nil
}

// Encrypting reports whether new evidence is written encrypted.
func (v *Vault) Encrypting() bool { return v != nil && v.EncryptNew && v.current != nil }

// KeyID identifies the current key (to compare without showing it).
func (v *Vault) KeyID() string {
	if v == nil || v.current == nil {
		return ""
	}
	return v.current.id
}

func (v *Vault) byID(id string) *keySet {
	if v == nil {
		return nil
	}
	for _, k := range []*keySet{v.current, v.previous} {
		if k != nil && k.id == id {
			return k
		}
	}
	return nil
}

// ─── archivos ───────────────────────────────────────────────────────────────

func nonce(prefix []byte, counter uint32, last bool) []byte {
	n := make([]byte, 12)
	copy(n, prefix)
	binary.BigEndian.PutUint32(n[7:11], counter)
	if last {
		n[11] = 1
	}
	return n
}

// writer seals the chunks of one file as they come.
type writer struct {
	dst     io.Writer
	aead    cipher.AEAD
	header  []byte
	prefix  []byte
	buf     []byte
	counter uint32
	closed  bool
}

// NewWriter encrypts what is written to it into dst with the current key; Close seals the final
// chunk (without Close the file cannot be opened).
func (v *Vault) NewWriter(dst io.Writer) (io.WriteCloser, error) {
	if v == nil || v.current == nil {
		return nil, ErrNoKey
	}
	prefix := make([]byte, 7)
	if _, err := rand.Read(prefix); err != nil {
		return nil, err
	}
	header := make([]byte, 0, headerLen)
	header = append(header, magic...)
	header = append(header, version)
	id, _ := hex.DecodeString(v.current.id)
	header = append(header, id...)
	header = append(header, prefix...)
	if _, err := dst.Write(header); err != nil {
		return nil, err
	}
	return &writer{dst: dst, aead: v.current.files, header: header, prefix: prefix, buf: make([]byte, 0, chunkPlain)}, nil
}

func (w *writer) Write(p []byte) (int, error) {
	n := 0
	for len(p) > 0 {
		// un bloque lleno se sella solo cuando llega más: el último siempre se sella en Close
		if len(w.buf) == chunkPlain {
			if err := w.flush(false); err != nil {
				return n, err
			}
		}
		take := min(chunkPlain-len(w.buf), len(p))
		w.buf = append(w.buf, p[:take]...)
		p, n = p[take:], n+take
	}
	return n, nil
}

func (w *writer) flush(last bool) error {
	if w.counter == ^uint32(0) {
		return errors.New("file too big to encrypt")
	}
	ct := w.aead.Seal(nil, nonce(w.prefix, w.counter, last), w.buf, w.header)
	w.counter++
	w.buf = w.buf[:0]
	_, err := w.dst.Write(ct)
	return err
}

func (w *writer) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	return w.flush(true)
}

// Encrypted reports whether head (the first bytes of a file) starts like an encrypted file.
func Encrypted(head []byte) bool {
	return len(head) >= len(magic)+1 && string(head[:len(magic)]) == magic && head[len(magic)] == version
}

// reader opens the chunks of one file.
type reader struct {
	src     io.Reader
	aead    cipher.AEAD
	header  []byte
	prefix  []byte
	counter uint32
	plain   []byte
	next    []byte // un bloque leído por adelantado: así se sabe cuál es el último
	done    bool
	err     error
}

// NewReader decrypts a file written by NewWriter. A file that is not encrypted is returned as is
// (evidence written before encryption was turned on, or with it off).
func (v *Vault) NewReader(src io.Reader) (io.Reader, error) {
	head := make([]byte, headerLen)
	n, err := io.ReadFull(src, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, err
	}
	head = head[:n]
	if !Encrypted(head) {
		return io.MultiReader(bytes.NewReader(head), src), nil
	}
	if n < headerLen {
		return nil, ErrCorrupt
	}
	ks := v.byID(hex.EncodeToString(head[len(magic)+1 : len(magic)+9]))
	if ks == nil {
		return nil, ErrNoKey
	}
	r := &reader{src: src, aead: ks.files, header: head, prefix: head[len(magic)+9:]}
	r.next, r.err = r.readChunk()
	if r.err == nil && len(r.next) == 0 {
		r.err = ErrCorrupt // sin el bloque final: truncado
	}
	return r, nil
}

func (r *reader) readChunk() ([]byte, error) {
	buf := make([]byte, chunkPlain+tagLen)
	n, err := io.ReadFull(r.src, buf)
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		err = nil
	}
	return buf[:n], err
}

func (r *reader) Read(p []byte) (int, error) {
	for len(r.plain) == 0 {
		if r.err != nil {
			return 0, r.err
		}
		if r.done {
			return 0, io.EOF
		}
		cur := r.next
		following, err := r.readChunk()
		if err != nil {
			r.err = err
			return 0, err
		}
		last := len(following) == 0
		plain, err := r.aead.Open(nil, nonce(r.prefix, r.counter, last), cur, r.header)
		if err != nil {
			r.err = ErrCorrupt
			return 0, r.err
		}
		r.counter++
		r.plain, r.next, r.done = plain, following, last
	}
	n := copy(p, r.plain)
	r.plain = r.plain[n:]
	return n, nil
}

// Encrypt returns data encrypted (a whole file in memory: screenshots).
func (v *Vault) Encrypt(data []byte) ([]byte, error) {
	var out bytes.Buffer
	w, err := v.NewWriter(&out)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// Decrypt returns the plaintext of a file read whole (plain files come back as they are).
func (v *Vault) Decrypt(data []byte) ([]byte, error) {
	r, err := v.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}

// ─── valores de texto (DOM) ─────────────────────────────────────────────────

// Sealed reports whether a stored text value is sealed.
func Sealed(s string) bool { return strings.HasPrefix(s, sealPrefix) }

// Seal encrypts a text value with the current key; name is bound as additional data (a value
// cannot be moved to another row).
func (v *Vault) Seal(name, plain string) (string, error) {
	if v == nil || v.current == nil {
		return "", ErrNoKey
	}
	nb := make([]byte, 12)
	if _, err := rand.Read(nb); err != nil {
		return "", err
	}
	ct := v.current.dom.Seal(nil, nb, []byte(plain), []byte(name))
	return sealPrefix + v.current.id + ":" + base64.RawStdEncoding.EncodeToString(append(nb, ct...)), nil
}

// Open decrypts a sealed value; a value that is not sealed is returned as is.
func (v *Vault) Open(name, stored string) (string, error) {
	if !Sealed(stored) {
		return stored, nil
	}
	rest := strings.TrimPrefix(stored, sealPrefix)
	id, b64, ok := strings.Cut(rest, ":")
	if !ok {
		return "", ErrCorrupt
	}
	ks := v.byID(id)
	if ks == nil {
		return "", ErrNoKey
	}
	raw, err := base64.RawStdEncoding.DecodeString(b64)
	if err != nil || len(raw) < 12+tagLen {
		return "", ErrCorrupt
	}
	plain, err := ks.dom.Open(nil, raw[:12], raw[12:], []byte(name))
	if err != nil {
		return "", ErrCorrupt
	}
	return string(plain), nil
}

// FileKeyID returns the key id of an encrypted file header ("" if head is not encrypted).
func FileKeyID(head []byte) string {
	if !Encrypted(head) || len(head) < len(magic)+9 {
		return ""
	}
	return hex.EncodeToString(head[len(magic)+1 : len(magic)+9])
}

// SealedKeyID returns the key id of a sealed value ("" if it is not sealed).
func SealedKeyID(stored string) string {
	if !Sealed(stored) {
		return ""
	}
	id, _, _ := strings.Cut(strings.TrimPrefix(stored, sealPrefix), ":")
	return id
}

// CanOpen reports whether the key with this id is configured (current or previous).
func (v *Vault) CanOpen(id string) bool { return v.byID(id) != nil }
