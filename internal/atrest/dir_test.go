package atrest

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestDirEncryptsAndSeeks(t *testing.T) {
	d := Dir{Path: t.TempDir(), Vault: vault(t, key(1), nil)}
	for _, n := range []int{0, 5, chunkPlain, 2*chunkPlain + 99} {
		data := make([]byte, n)
		rand.Read(data)
		if err := d.WriteFile("f.webm", data); err != nil {
			t.Fatal(err)
		}
		raw, _ := os.ReadFile(filepath.Join(d.Path, "f.webm"))
		if !Encrypted(raw) {
			t.Fatalf("%d: se escribió en claro", n)
		}
		f, err := d.Open("f.webm")
		if err != nil {
			t.Fatal(err)
		}
		if f.Size() != int64(n) {
			t.Fatalf("tamaño %d, quería %d", f.Size(), n)
		}
		got, err := io.ReadAll(f)
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("%d: %v", n, err)
		}
		// un Range en medio, cruzando el borde de un bloque
		if n > chunkPlain+10 {
			off := int64(chunkPlain - 10)
			f.Seek(off, io.SeekStart)
			part := make([]byte, 30)
			io.ReadFull(f, part)
			if !bytes.Equal(part, data[off:off+30]) {
				t.Fatal("Seek devolvió otros bytes")
			}
			if end, _ := f.Seek(-3, io.SeekEnd); end != int64(n-3) {
				t.Fatalf("SeekEnd %d", end)
			}
		}
		f.Close()
	}
}

// Con el cifrado apagado (o sin Vault) se escribe y lee en claro, como antes.
func TestDirPlainWhenOff(t *testing.T) {
	readOnly, _ := New(key(1), nil, false)
	for _, v := range []*Vault{nil, readOnly} {
		d := Dir{Path: t.TempDir(), Vault: v}
		d.WriteFile("a.png", []byte("\x89PNG imagen"))
		raw, _ := os.ReadFile(filepath.Join(d.Path, "a.png"))
		if string(raw) != "\x89PNG imagen" {
			t.Fatal("con el cifrado apagado el archivo cambió")
		}
		if got, err := d.ReadFile("a.png"); err != nil || string(got) != "\x89PNG imagen" {
			t.Fatal(err)
		}
	}
}

// Las capturas en claro que había antes de activar el cifrado se siguen leyendo.
func TestDirReadsOldPlainFiles(t *testing.T) {
	d := Dir{Path: t.TempDir(), Vault: vault(t, key(1), nil)}
	os.WriteFile(filepath.Join(d.Path, "vieja.png"), []byte("\x89PNG vieja"), 0o600)
	if got, err := d.ReadFile("vieja.png"); err != nil || string(got) != "\x89PNG vieja" {
		t.Fatal(err)
	}
}

func TestDirDetectsTamperingAndMissingKey(t *testing.T) {
	d := Dir{Path: t.TempDir(), Vault: vault(t, key(1), nil)}
	data := make([]byte, 3*chunkPlain)
	rand.Read(data)
	d.WriteFile("v.mp4", data)
	p := filepath.Join(d.Path, "v.mp4")
	raw, _ := os.ReadFile(p)

	raw[headerLen+chunkPlain+tagLen+5] ^= 1 // un bit del segundo bloque
	os.WriteFile(p, raw, 0o600)
	if _, err := d.ReadFile("v.mp4"); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("bit cambiado: %v", err)
	}
	raw[headerLen+chunkPlain+tagLen+5] ^= 1
	for name, bad := range map[string][]byte{
		"sin el último bloque": raw[:len(raw)-(chunkPlain+tagLen)],
		"cortado":              raw[:len(raw)-7],
		"bloque repetido":      append(append([]byte{}, raw...), raw[headerLen:headerLen+chunkPlain+tagLen]...),
		"solo header":          raw[:headerLen],
	} {
		os.WriteFile(p, bad, 0o600)
		if _, err := d.ReadFile("v.mp4"); err == nil {
			t.Errorf("%s: se leyó sin error", name)
		}
	}
	os.WriteFile(p, raw, 0o600)
	other := Dir{Path: d.Path, Vault: vault(t, key(2), nil)}
	if _, err := other.Open("v.mp4"); !errors.Is(err, ErrNoKey) {
		t.Fatalf("otra clave: %v", err)
	}
	if _, err := (Dir{Path: d.Path}).Open("v.mp4"); !errors.Is(err, ErrNoKey) {
		t.Fatalf("sin clave: %v", err)
	}
}

func TestDirRejectsPaths(t *testing.T) {
	d := Dir{Path: t.TempDir()}
	for _, name := range []string{"", ".", "..", "../x", "a/b", filepath.Join("..", "x")} {
		if _, err := d.Open(name); err == nil {
			t.Errorf("%q abierto", name)
		}
		if err := d.WriteFile(name, nil); err == nil {
			t.Errorf("%q escrito", name)
		}
	}
}

// Activar el cifrado después y rotar la clave: los archivos se reescriben sin perder nada.
func TestDirResealPlainAndRotation(t *testing.T) {
	dir := t.TempDir()
	plain := Dir{Path: dir}
	plain.WriteFile("vieja.png", []byte("\x89PNG en claro"))
	k1 := Dir{Path: dir, Vault: vault(t, key(1), nil)}
	if changed, err := k1.Reseal("vieja.png"); !changed || err != nil {
		t.Fatalf("en claro: %v %v", changed, err)
	}
	if id, _ := k1.KeyOf("vieja.png"); id != k1.Vault.KeyID() {
		t.Fatal("no quedó cifrada con la clave actual")
	}
	if changed, _ := k1.Reseal("vieja.png"); changed {
		t.Fatal("ya cifrada con la clave actual: no se reescribe")
	}
	k2 := Dir{Path: dir, Vault: vault(t, key(2), key(1))}
	if changed, err := k2.Reseal("vieja.png"); !changed || err != nil {
		t.Fatalf("rotación: %v %v", changed, err)
	}
	only2 := Dir{Path: dir, Vault: vault(t, key(2), nil)}
	if got, err := only2.ReadFile("vieja.png"); err != nil || string(got) != "\x89PNG en claro" {
		t.Fatalf("tras rotar, la clave nueva sola la abre: %v", err)
	}
	other := Dir{Path: dir, Vault: vault(t, key(3), nil)}
	if _, err := other.Reseal("vieja.png"); !errors.Is(err, ErrNoKey) {
		t.Fatalf("clave desconocida: %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("quedaron temporales: %v", entries)
	}
}
