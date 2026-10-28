package atrest

import (
	"crypto/cipher"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Dir is the evidence folder (screenshots, traces and videos) seen through the Vault: what is
// written is encrypted when the Vault encrypts, and what is read is decrypted when it was. With a
// nil Vault it is plain file access, as before encryption existed.
type Dir struct {
	Path  string
	Vault *Vault
}

// File is an evidence file opened for reading: its plaintext, with Seek (a video is served with
// Range requests without decrypting it whole).
type File interface {
	io.ReadSeekCloser
	Size() int64
	ModTime() time.Time
}

// path keeps name inside the folder (only a bare file name is accepted).
func (d Dir) path(name string) (string, error) {
	if name == "" || name == "." || strings.Contains(name, "..") || strings.ContainsAny(name, `/\`) || name != filepath.Base(name) {
		return "", os.ErrNotExist
	}
	return filepath.Join(d.Path, name), nil
}

// WriteFile writes a whole file (0600: only the server reads the evidence).
func (d Dir) WriteFile(name string, data []byte) error {
	w, err := d.Create(name)
	if err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

// Create opens a new file to write; Close finishes it (with encryption, it seals the last chunk).
func (d Dir) Create(name string) (io.WriteCloser, error) {
	p, err := d.path(name)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	if !d.Vault.Encrypting() {
		return f, nil
	}
	w, err := d.Vault.NewWriter(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	return &sealedFile{w: w, f: f}, nil
}

type sealedFile struct {
	w io.WriteCloser
	f *os.File
}

func (s *sealedFile) Write(p []byte) (int, error) { return s.w.Write(p) }

func (s *sealedFile) Close() error {
	err := s.w.Close()
	if cerr := s.f.Close(); err == nil {
		err = cerr
	}
	return err
}

// ReadFile reads a whole file, decrypted.
func (d Dir) ReadFile(name string) ([]byte, error) {
	f, err := d.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// Open opens a file to read its plaintext: a plain file (written before encryption, or with it
// off) is read as is; an encrypted one needs its key (ErrNoKey) and is checked chunk by chunk.
func (d Dir) Open(name string) (File, error) {
	p, err := d.path(name)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	head := make([]byte, headerLen)
	n, _ := f.ReadAt(head, 0)
	if !Encrypted(head[:n]) {
		return plainFile{f, st}, nil
	}
	sr, err := d.Vault.seekReader(f, head[:n], st.Size())
	if err != nil {
		f.Close()
		return nil, err
	}
	sr.mod, sr.closer = st.ModTime(), f
	return sr, nil
}

type plainFile struct {
	*os.File
	st os.FileInfo
}

func (p plainFile) Size() int64        { return p.st.Size() }
func (p plainFile) ModTime() time.Time { return p.st.ModTime() }

// seekReader decrypts an encrypted file at any position: the chunks have a fixed size, so the
// chunk of an offset is known, and each one is authenticated (with its position and whether it
// is the last) before any of its bytes is returned.
type seekReader struct {
	src     io.ReaderAt
	closer  io.Closer
	aead    cipher.AEAD
	header  []byte
	prefix  []byte
	chunks  int64
	lastLen int64 // bytes cifrados del último bloque (con su tag)
	size    int64
	pos     int64
	cached  int64 // índice del bloque en plain (-1: ninguno)
	plain   []byte
	mod     time.Time
}

func (v *Vault) seekReader(src io.ReaderAt, head []byte, fileSize int64) (*seekReader, error) {
	if len(head) < headerLen {
		return nil, ErrCorrupt
	}
	ks := v.byID(hex.EncodeToString(head[len(magic)+1 : len(magic)+9]))
	if ks == nil {
		return nil, ErrNoKey
	}
	body, full := fileSize-int64(headerLen), int64(chunkPlain+tagLen)
	if body < tagLen {
		return nil, ErrCorrupt
	}
	chunks := (body + full - 1) / full
	lastLen := body - (chunks-1)*full
	if lastLen < tagLen || chunks > 1<<32 {
		return nil, ErrCorrupt
	}
	return &seekReader{
		src: src, aead: ks.files, header: append([]byte(nil), head...), prefix: append([]byte(nil), head[len(magic)+9:]...),
		chunks: chunks, lastLen: lastLen, size: (chunks-1)*chunkPlain + lastLen - tagLen, cached: -1,
	}, nil
}

func (r *seekReader) Size() int64        { return r.size }
func (r *seekReader) ModTime() time.Time { return r.mod }

func (r *seekReader) Close() error {
	if r.closer == nil {
		return nil
	}
	return r.closer.Close()
}

func (r *seekReader) load(idx int64) error {
	if r.cached == idx {
		return nil
	}
	n := int64(chunkPlain + tagLen)
	last := idx == r.chunks-1
	if last {
		n = r.lastLen
	}
	ct := make([]byte, n)
	if _, err := r.src.ReadAt(ct, int64(headerLen)+idx*int64(chunkPlain+tagLen)); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	plain, err := r.aead.Open(ct[:0], nonce(r.prefix, uint32(idx), last), ct, r.header)
	if err != nil {
		return ErrCorrupt
	}
	r.plain, r.cached = plain, idx
	return nil
}

func (r *seekReader) Read(p []byte) (int, error) {
	if r.pos >= r.size {
		// un archivo vacío también se autentica: su único bloque es el final
		if r.size == 0 && r.cached < 0 {
			if err := r.load(0); err != nil {
				return 0, err
			}
		}
		return 0, io.EOF
	}
	idx := r.pos / chunkPlain
	if err := r.load(idx); err != nil {
		return 0, err
	}
	n := copy(p, r.plain[r.pos-idx*chunkPlain:])
	r.pos += int64(n)
	return n, nil
}

func (r *seekReader) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		offset += r.pos
	case io.SeekEnd:
		offset += r.size
	default:
		return 0, errors.New("atrest: invalid whence")
	}
	if offset < 0 {
		return 0, errors.New("atrest: negative position")
	}
	r.pos = offset
	return offset, nil
}

// KeyOf says how a file is stored: "" = in clear, otherwise the id of the key that encrypted it.
func (d Dir) KeyOf(name string) (string, error) {
	p, err := d.path(name)
	if err != nil {
		return "", err
	}
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	head := make([]byte, headerLen)
	n, _ := io.ReadFull(f, head)
	return FileKeyID(head[:n]), nil
}

// Reseal rewrites a file encrypted with the current key when it is in clear or encrypted with the
// previous one (encryption turned on later, or a key rotation). The new file is written next to it
// and renamed over it: a failure halfway leaves the old one as it was. It reports whether it
// changed; ErrNoKey when the file was encrypted with a key that is not configured.
func (d Dir) Reseal(name string) (bool, error) {
	if !d.Vault.Encrypting() {
		return false, errors.New("atrest: reseal needs encryption on")
	}
	id, err := d.KeyOf(name)
	if err != nil || id == d.Vault.KeyID() {
		return false, err
	}
	src, err := d.Open(name)
	if err != nil {
		return false, err
	}
	defer src.Close()
	tmp := "." + name + ".reseal"
	w, err := d.Create(tmp)
	if err != nil {
		return false, err
	}
	tmpPath, _ := d.path(tmp)
	if _, err = io.Copy(w, src); err == nil {
		err = w.Close()
	} else {
		w.Close()
	}
	src.Close() // en Windows no se puede reemplazar un archivo abierto
	if err != nil {
		os.Remove(tmpPath)
		return false, err
	}
	dst, _ := d.path(name)
	if err := os.Rename(tmpPath, dst); err != nil {
		os.Remove(tmpPath)
		return false, err
	}
	return true, nil
}
