package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestIsLoopback(t *testing.T) {
	for bind, want := range map[string]bool{
		"127.0.0.1": true, "127.1.2.3": true, "::1": true, "[::1]": true, "localhost": true, "LocalHost": true, " 127.0.0.1 ": true,
		"": false, "0.0.0.0": false, "::": false, "[::]": false, "192.168.1.10": false, "10.0.0.5": false, "fe80::1": false,
		"example.com": false, "localhost.example.com": false, "127.0.0.1.nip.io": false,
	} {
		if got := isLoopback(bind); got != want {
			t.Errorf("isLoopback(%q) = %v", bind, got)
		}
	}
}

// Matriz: bind × credenciales × TRACEREPORTS_INSECURE × Docker → dónde escucha o si no arranca.
func TestCheckExposureMatrix(t *testing.T) {
	creds := map[string]exposure{
		"ninguna":                  {},
		"token":                    {token: true},
		"ingest token":             {token: true},
		"login UI":                 {uiLogin: true},
		"token y login":            {token: true, uiLogin: true},
		"usuario sin clave (nada)": {},
	}
	for _, bind := range []string{"", "0.0.0.0", "::", "192.168.1.10", "127.0.0.1", "::1", "localhost"} {
		for name, c := range creds {
			for _, insecure := range []bool{false, true} {
				for _, docker := range []bool{false, true} {
					e := c
					e.bind, e.insecure, e.docker = bind, insecure, docker
					protected := c.token || c.uiLogin
					binds, err := checkExposure(e)
					label := fmt.Sprintf("bind=%q creds=%s insecure=%v docker=%v", bind, name, insecure, docker)
					switch {
					case isLoopback(bind) || protected || insecure:
						if err != nil || !reflect.DeepEqual(binds, bindAddrs(bind)) {
							t.Errorf("%s: debe escuchar en %q: %v %v", label, bind, binds, err)
						}
					case bind == "" && !docker:
						if err != nil || !reflect.DeepEqual(binds, localBinds) {
							t.Errorf("%s: sin credenciales escucha solo en este equipo: %v %v", label, binds, err)
						}
					default:
						if err == nil || binds != nil {
							t.Errorf("%s: no debe arrancar: %v", label, binds)
						}
					}
				}
			}
		}
	}
}

func TestExposureMessages(t *testing.T) {
	_, err := checkExposure(exposure{bind: "0.0.0.0"})
	if err == nil || !strings.Contains(err.Error(), "TRACEREPORTS_TOKEN=$(openssl rand -hex 32)") ||
		!strings.Contains(err.Error(), "TRACEREPORTS_INSECURE=1") || !strings.Contains(err.Error(), "TRACEREPORTS_BIND=127.0.0.1") {
		t.Fatalf("el error debe traer el snippet: %v", err)
	}
	_, err = checkExposure(exposure{docker: true})
	if err == nil || !strings.Contains(err.Error(), "inside Docker") || !strings.Contains(err.Error(), "--env-file") ||
		strings.Contains(err.Error(), "TRACEREPORTS_BIND=127.0.0.1") {
		t.Fatalf("en Docker, 127.0.0.1 no es una salida (no se alcanza desde el host): %v", err)
	}
}

func TestExposureFromEnv(t *testing.T) {
	restoreEnv(t)
	for _, k := range []string{"TRACEREPORTS_BIND", "TRACEREPORTS_RUNTIME", "TRACEREPORTS_TOKEN", "TRACEREPORTS_INGEST_TOKEN",
		"TRACEREPORTS_UI_USER", "TRACEREPORTS_UI_PASSWORD", "TRACEREPORTS_INSECURE"} {
		os.Unsetenv(k)
	}
	os.Setenv("TRACEREPORTS_UI_USER", "admin") // sin clave no es login
	os.Setenv("TRACEREPORTS_INGEST_TOKEN", "ci")
	os.Setenv("TRACEREPORTS_RUNTIME", "docker")
	os.Setenv("TRACEREPORTS_INSECURE", "true")
	os.Setenv("TRACEREPORTS_BIND", " 0.0.0.0 ")
	if got := exposureFromEnv(); got != (exposure{bind: "0.0.0.0", docker: true, token: true, insecure: true}) {
		t.Fatalf("%+v", got)
	}
}

// startEnv prepara un servidor aislado (sin .env, sin IA ni notificaciones) en un puerto libre.
func startEnv(t *testing.T) (port int, dataDir string) {
	t.Helper()
	restoreEnv(t)
	port, err := freePort(0, 8400)
	if err != nil {
		t.Fatal(err)
	}
	dataDir = filepath.Join(t.TempDir(), "data")
	demoEnv(dataDir, port, false)
	os.Unsetenv("TRACEREPORTS_BIND")
	os.Unsetenv("TRACEREPORTS_DEMO")
	return port, dataDir
}

// Escuchando en la red sin credenciales, el servidor no arranca ni toca los datos.
func TestServerRefusesToStartExposed(t *testing.T) {
	for _, env := range []map[string]string{
		{"TRACEREPORTS_BIND": "0.0.0.0"},
		{"TRACEREPORTS_BIND": "::"},
		{"TRACEREPORTS_RUNTIME": "docker"},
		{"TRACEREPORTS_BIND": "0.0.0.0", "TRACEREPORTS_UI_USER": "admin"}, // login incompleto
	} {
		t.Run(fmt.Sprint(env), func(t *testing.T) {
			_, dataDir := startEnv(t)
			for k, v := range env {
				os.Setenv(k, v)
			}
			if k := "TRACEREPORTS_RUNTIME"; env[k] == "docker" {
				os.Setenv("DATA_DIR", dataDir) // el aviso del volumen no importa aquí
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			err := runServer(ctx)
			if err == nil || !strings.Contains(err.Error(), "refusing to start") || !strings.Contains(err.Error(), "openssl rand -hex 32") {
				t.Fatalf("debe negarse a arrancar: %v", err)
			}
			if _, statErr := os.Stat(dataDir); !os.IsNotExist(statErr) {
				t.Fatalf("no debe crear DATA_DIR antes de negarse: %v", statErr)
			}
		})
	}
}

// startServer arranca runServer y espera que responda en addr; devuelve una función para pararlo.
func startServer(t *testing.T, addr string) {
	t.Helper()
	ctx, stop := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- runServer(ctx) }()
	t.Cleanup(func() { stop(); <-errCh })
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-errCh:
			t.Fatalf("el servidor terminó: %v", err)
		default:
		}
		if res, err := http.Get("http://" + addr + "/api/v1/config"); err == nil {
			res.Body.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("el servidor no respondió en %s", addr)
}

// Sin credenciales ni TRACEREPORTS_BIND (el uso local de siempre): arranca, solo en este equipo.
func TestServerWithoutCredentialsListensOnlyLocally(t *testing.T) {
	port, _ := startEnv(t)
	startServer(t, fmt.Sprintf("127.0.0.1:%d", port))
	if ip := nonLoopbackIP(); ip != "" {
		c, err := net.DialTimeout("tcp", net.JoinHostPort(ip, fmt.Sprint(port)), 2*time.Second)
		if err == nil {
			c.Close()
			t.Fatalf("sin credenciales no debe escuchar en %s", ip)
		}
	}
}

// Con un token, o con TRACEREPORTS_INSECURE=1, escucha en todas las interfaces.
func TestServerOnTheNetworkWithTokenOrInsecure(t *testing.T) {
	for _, env := range []map[string]string{
		{"TRACEREPORTS_BIND": "0.0.0.0", "TRACEREPORTS_TOKEN": "s3cret-token"},
		{"TRACEREPORTS_BIND": "0.0.0.0", "TRACEREPORTS_INSECURE": "1"},
	} {
		t.Run(fmt.Sprint(env), func(t *testing.T) {
			port, _ := startEnv(t)
			for k, v := range env {
				os.Setenv(k, v)
			}
			addr := fmt.Sprintf("127.0.0.1:%d", port)
			if ip := nonLoopbackIP(); ip != "" {
				addr = net.JoinHostPort(ip, fmt.Sprint(port))
			}
			startServer(t, addr)
		})
	}
}

// 127.0.0.1 y localhost escuchan también en ::1 (solo este equipo); el resto, tal cual.
func TestBindAddrs(t *testing.T) {
	for bind, want := range map[string][]string{
		"127.0.0.1": localBinds, " LocalHost ": localBinds,
		"::1": {"::1"}, "127.0.0.2": {"127.0.0.2"}, "0.0.0.0": {"0.0.0.0"}, "": {""}, "192.168.1.10": {"192.168.1.10"},
	} {
		if got := bindAddrs(bind); !reflect.DeepEqual(got, want) {
			t.Errorf("bindAddrs(%q) = %v, quiero %v", bind, got, want)
		}
	}
}

// demo y selftest siguen arrancando sin credenciales: escuchan en 127.0.0.1 y ::1.
func TestDemoEnvStillStartsWithoutCredentials(t *testing.T) {
	restoreEnv(t)
	port, err := freePort(0, 8450)
	if err != nil {
		t.Fatal(err)
	}
	demoEnv(t.TempDir(), port, false)
	if binds, err := checkExposure(exposureFromEnv()); err != nil || !reflect.DeepEqual(binds, localBinds) {
		t.Fatalf("demo: %v %v", binds, err)
	}
	startServer(t, fmt.Sprintf("127.0.0.1:%d", port))
}

// nonLoopbackIP returns an IPv4 address of this machine reachable from the network ("" if none).
func nonLoopbackIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() && !n.IP.IsLinkLocalUnicast() {
			return n.IP.String()
		}
	}
	return ""
}
