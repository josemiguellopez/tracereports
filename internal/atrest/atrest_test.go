package atrest

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func key(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func vault(t testing.TB, cur, prev []byte) *Vault {
	t.Helper()
	v, err := New(cur, prev, true)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestRoundTripSizes(t *testing.T) {
	v := vault(t, key(1), nil)
	for _, n := range []int{0, 1, chunkPlain - 1, chunkPlain, chunkPlain + 1, 3*chunkPlain + 17} {
		data := make([]byte, n)
		rand.Read(data)
		enc, err := v.Encrypt(data)
		if err != nil {
			t.Fatal(err)
		}
		if !Encrypted(enc) || FileKeyID(enc) != v.KeyID() {
			t.Fatalf("%d: sin header", n)
		}
		if n > 16 && bytes.Contains(enc, data[:16]) {
			t.Fatalf("%d: el texto en claro aparece en el cifrado", n)
		}
		got, err := v.Decrypt(enc)
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("%d bytes: %v", n, err)
		}
	}
}

// Escrito en pedazos de cualquier tamaño, sale igual (sin ciclos con bloques justo llenos).
func TestStreamingWritesOfAnySize(t *testing.T) {
	v := vault(t, key(1), nil)
	data := make([]byte, 5*chunkPlain+123)
	rand.Read(data)
	for _, step := range []int{1, 7, chunkPlain, chunkPlain / 2, chunkPlain + 3} {
		var out bytes.Buffer
		w, _ := v.NewWriter(&out)
		for i := 0; i < len(data); i += step {
			w.Write(data[i:min(i+step, len(data))])
		}
		w.Close()
		r, err := v.NewReader(&out)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(r)
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("paso %d: %v", step, err)
		}
	}
}

func TestNonceIsUniquePerFile(t *testing.T) {
	v := vault(t, key(1), nil)
	a, _ := v.Encrypt([]byte("igual"))
	b, _ := v.Encrypt([]byte("igual"))
	if bytes.Equal(a, b) || bytes.Equal(a[headerLen-7:headerLen], b[headerLen-7:headerLen]) {
		t.Fatal("el mismo contenido debe cifrarse distinto (prefijo de nonce aleatorio)")
	}
}

// Manipular, truncar, extender o reordenar un archivo cifrado se detecta (autenticación de GCM).
func TestTamperingIsDetected(t *testing.T) {
	v := vault(t, key(1), nil)
	data := make([]byte, 2*chunkPlain+10)
	rand.Read(data)
	enc, _ := v.Encrypt(data)
	c0, c1 := headerLen, headerLen+chunkPlain+tagLen
	cases := map[string][]byte{
		"un bit":            append(append([]byte{}, enc[:100]...), append([]byte{enc[100] ^ 1}, enc[101:]...)...),
		"header":            append(append([]byte{}, enc[:headerLen-1]...), append([]byte{enc[headerLen-1] ^ 1}, enc[headerLen:]...)...),
		"truncado":          enc[:c1+chunkPlain+tagLen-tagLen*2],
		"sin el final":      enc[:c1+chunkPlain+tagLen],
		"bloque extra":      append(append([]byte{}, enc...), enc[c0:c1]...),
		"bloques cambiados": append(append(append([]byte{}, enc[:c0]...), enc[c1:c1+chunkPlain+tagLen]...), append(enc[c0:c1], enc[c1+chunkPlain+tagLen:]...)...),
		"solo header":       enc[:headerLen],
	}
	for name, bad := range cases {
		if _, err := v.Decrypt(bad); !errors.Is(err, ErrCorrupt) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestPlainFilesPassThrough(t *testing.T) {
	v := vault(t, key(1), nil)
	png := []byte("\x89PNG\r\n\x1a\nimagen-vieja")
	for _, vv := range []*Vault{v, nil} {
		got, err := vv.Decrypt(png)
		if err != nil || !bytes.Equal(got, png) {
			t.Fatalf("un archivo en claro se devuelve tal cual: %v", err)
		}
	}
	var nilVault *Vault
	enc, _ := v.Encrypt(png)
	if _, err := nilVault.Decrypt(enc); !errors.Is(err, ErrNoKey) {
		t.Fatalf("sin clave: %v", err)
	}
}

// La clave anterior abre lo que cifró (rotación); otra clave no.
func TestRotationWithPreviousKey(t *testing.T) {
	old := vault(t, key(1), nil)
	enc, _ := old.Encrypt([]byte("evidencia"))
	sealed, _ := old.Seal("dom:7", "snapshot")
	rotated := vault(t, key(2), key(1))
	if got, err := rotated.Decrypt(enc); err != nil || string(got) != "evidencia" {
		t.Fatalf("archivo con la clave anterior: %v", err)
	}
	if got, err := rotated.Open("dom:7", sealed); err != nil || got != "snapshot" {
		t.Fatalf("DOM con la clave anterior: %v", err)
	}
	other := vault(t, key(3), nil)
	if _, err := other.Decrypt(enc); !errors.Is(err, ErrNoKey) {
		t.Fatalf("otra clave: %v", err)
	}
	if _, err := other.Open("dom:7", sealed); !errors.Is(err, ErrNoKey) {
		t.Fatalf("otra clave (DOM): %v", err)
	}
}

func TestSealBindsTheName(t *testing.T) {
	v := vault(t, key(1), nil)
	s, _ := v.Seal("dom:1", `{"elements":[]}`)
	if !Sealed(s) || SealedKeyID(s) != v.KeyID() || strings.Contains(s, "elements") {
		t.Fatalf("%s", s)
	}
	if got, err := v.Open("dom:1", s); err != nil || got != `{"elements":[]}` {
		t.Fatalf("%v", err)
	}
	if _, err := v.Open("dom:2", s); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("otra fila: %v", err)
	}
	if got, _ := v.Open("dom:1", "en claro"); got != "en claro" {
		t.Fatal("un valor sin sellar se devuelve tal cual")
	}
}

// Subclaves distintas por uso: lo cifrado como archivo no se abre como DOM y viceversa.
func TestSubkeysPerPurpose(t *testing.T) {
	ks, _ := newKeySet(key(1))
	n := make([]byte, 12)
	ct := ks.files.Seal(nil, n, []byte("x"), nil)
	if _, err := ks.dom.Open(nil, n, ct, nil); err == nil {
		t.Fatal("la subclave de archivos abre lo del DOM")
	}
}

func TestFromEnv(t *testing.T) {
	data := t.TempDir()
	reset := func() {
		for _, k := range []string{"ENCRYPT_AT_REST", "DB_KEY", "DB_KEY_FILE", "DB_KEY_PREVIOUS", "DB_KEY_PREVIOUS_FILE"} {
			t.Setenv("TRACEREPORTS_"+k, "")
		}
	}
	reset()
	if v, err := FromEnv(data); v != nil || err != nil {
		t.Fatalf("sin nada: %v %v", v, err)
	}
	t.Setenv("TRACEREPORTS_ENCRYPT_AT_REST", "1")
	if _, err := FromEnv(data); err == nil || !strings.Contains(err.Error(), "needs TRACEREPORTS_DB_KEY") {
		t.Fatalf("activo sin clave no arranca: %v", err)
	}
	t.Setenv("TRACEREPORTS_DB_KEY", "corta")
	if _, err := FromEnv(data); err == nil {
		t.Fatal("clave inválida")
	}
	t.Setenv("TRACEREPORTS_DB_KEY", "")
	inData := filepath.Join(data, "key")
	os.WriteFile(inData, []byte("AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE="), 0o600)
	t.Setenv("TRACEREPORTS_DB_KEY_FILE", inData)
	if _, err := FromEnv(data); err == nil || !strings.Contains(err.Error(), "inside DATA_DIR") {
		t.Fatalf("clave dentro de DATA_DIR: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "key")
	os.WriteFile(outside, []byte("AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=\n"), 0o600)
	t.Setenv("TRACEREPORTS_DB_KEY_FILE", outside)
	v, err := FromEnv(data)
	if err != nil || !v.Encrypting() || v.KeyID() == "" {
		t.Fatalf("archivo fuera de DATA_DIR: %v", err)
	}
	t.Setenv("TRACEREPORTS_DB_KEY", "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=")
	if _, err := FromEnv(data); err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("las dos: %v", err)
	}
	// con clave y el cifrado apagado: solo lee
	reset()
	t.Setenv("TRACEREPORTS_DB_KEY", strings.Repeat("01", 32))
	if v, err := FromEnv(data); err != nil || v == nil || v.Encrypting() {
		t.Fatalf("solo lectura: %v %v", v, err)
	}
}

func FuzzDecryptFile(f *testing.F) {
	v := vault(f, key(1), nil)
	enc, _ := v.Encrypt([]byte("evidencia de prueba"))
	f.Add(enc)
	f.Add([]byte("TRENC\x01"))
	f.Add([]byte("\x89PNG"))
	f.Add(enc[:headerLen+3])
	f.Fuzz(func(t *testing.T, data []byte) {
		got, err := v.Decrypt(data)
		if err == nil && Encrypted(data) && bytes.Equal(data, enc) && string(got) != "evidencia de prueba" {
			t.Fatal("descifrado distinto")
		}
	})
}

func BenchmarkEncrypt(b *testing.B) {
	v := vault(b, key(1), nil)
	data := make([]byte, 4<<20)
	b.SetBytes(int64(len(data)))
	for i := 0; i < b.N; i++ {
		var out bytes.Buffer
		w, _ := v.NewWriter(&out)
		w.Write(data)
		w.Close()
	}
}
