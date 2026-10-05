// Ejemplo: tests E2E con playwright-go reportados a TraceReports (pasos, capturas, red del
// backend y AI Triage de los fallos).
//
//	cd examples/playwright-go
//	go test -v ./...                         # servidor en $TRACEREPORTS_URL o localhost:8080
//	BROWSER_CHANNEL=chrome go test -v ./...  # usa tu Chrome (no descarga navegadores)
//	HEADLESS=0 go test -v ./...              # ver el navegador
//
// La primera ejecución descarga el driver de Playwright (y Chromium si no usas un canal).
package orangehrm

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/mxschmitt/playwright-go"

	tracereports "github.com/josemiguellopez/tracereports/client/go"
)

const loginURL = "https://opensource-demo.orangehrmlive.com/web/index.php/auth/login"

var (
	browser playwright.Browser
	report  *tracereports.Client
	expect  = playwright.NewPlaywrightAssertions(15000)
)

func TestMain(m *testing.M) {
	channel := os.Getenv("BROWSER_CHANNEL")
	if err := playwright.Install(&playwright.RunOptions{SkipInstallBrowsers: channel != "", Browsers: []string{"chromium"}}); err != nil {
		fmt.Fprintln(os.Stderr, "instalando Playwright:", err)
		os.Exit(1)
	}
	pw, err := playwright.Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "iniciando Playwright:", err)
		os.Exit(1)
	}
	opts := playwright.BrowserTypeLaunchOptions{Headless: playwright.Bool(os.Getenv("HEADLESS") != "0")}
	if channel != "" {
		opts.Channel = playwright.String(channel)
	}
	if browser, err = pw.Chromium.Launch(opts); err != nil {
		fmt.Fprintln(os.Stderr, "lanzando el navegador:", err)
		os.Exit(1)
	}

	report = tracereports.New("")
	if channel == "" {
		channel = "chromium"
	}
	report.StartRun("OrangeHRM - Playwright Go", "demo pública · "+channel)

	code := m.Run()

	report.FinishRun()
	if report.RunID != 0 {
		fmt.Println("Reporte:", report.ReportURL())
	}
	browser.Close()
	pw.Stop()
	os.Exit(code)
}

// e2e es el contexto de un test: página propia, captura de red y su test en TraceReports.
type e2e struct {
	t    *testing.T
	page playwright.Page
	rep  *tracereports.Test
	net  *netCapture
	err  string
}

// run crea la página, reporta el test y, al terminar, envía la red y el resultado.
func run(t *testing.T, category, description string, body func(x *e2e)) {
	ctx, err := browser.NewContext(playwright.BrowserNewContextOptions{Viewport: &playwright.Size{Width: 1366, Height: 768}})
	if err != nil {
		t.Fatal(err)
	}
	page, _ := ctx.NewPage()
	// identidad estable: paquete + nombre del test (incluye subtests); nil si el servidor no responde: es seguro
	rep, _ := report.StartTestWithKey(tracereports.Key("examples/playwright-go", t.Name()), t.Name(), category, description)
	x := &e2e{t: t, page: page, rep: rep, net: captureNetwork(page)}

	defer func() {
		status, msg := tracereports.Pass, ""
		if r := recover(); r != nil || t.Failed() {
			status, msg = tracereports.Fail, x.err
			if msg == "" {
				msg = fmt.Sprint(r)
			}
			x.shot("Captura al fallar", tracereports.Fail)
			// snapshot de los elementos: si falló un locator, el reporte sugiere reemplazos
			if snap, err := page.Evaluate(domScript); err == nil {
				rep.DOM(snap)
			}
		}
		rep.Network(x.net.all()) // antes de Finish: el AI Triage ve los errores de backend
		rep.Finish(status, msg, "")
		ctx.Close()
	}()
	body(x)
}

// step registra un paso; check falla el test con un mensaje claro (también va al reporte).
func (x *e2e) step(msg string) { x.rep.Info(msg) }

func (x *e2e) shot(msg, status string) {
	if png, err := x.page.Screenshot(); err == nil {
		x.rep.Screenshot(png, msg, status)
	}
}

func (x *e2e) check(err error, what string) {
	if err != nil {
		x.err = fmt.Sprintf("%s: %v", what, err)
		x.rep.Fail(x.err)
		x.t.Fatal(x.err)
	}
}

func (x *e2e) login(user, password string) {
	x.check(nilOnly(x.page.Goto(loginURL)), "abrir el login")
	x.check(x.page.Locator("input[name='username']").Fill(user), "escribir el usuario")
	x.check(x.page.Locator("input[name='password']").Fill(password), "escribir la contraseña")
	x.check(x.page.Locator("button[type='submit']").Click(), "presionar Login")
}

func nilOnly(_ playwright.Response, err error) error { return err }

// ─── tests ───────────────────────────────────────────────────────────────

func TestLoginCorrecto(t *testing.T) {
	run(t, "OrangeHRM, Login, Smoke", "El administrador inicia sesión y llega al Dashboard", func(x *e2e) {
		x.step("Abrir el formulario de login")
		x.login("Admin", "admin123")
		x.check(expect.Locator(x.page.Locator(".oxd-topbar-header-breadcrumb h6")).ToHaveText("Dashboard"), "llegar al Dashboard")
		x.shot("Dashboard visible", tracereports.Pass)
	})
}

func TestLoginPasswordIncorrecta(t *testing.T) {
	run(t, "OrangeHRM, Login, Negativo", "Una contraseña incorrecta muestra 'Invalid credentials'", func(x *e2e) {
		x.login("Admin", "clave-incorrecta")
		x.check(expect.Locator(x.page.Locator(".oxd-alert-content-text")).ToHaveText("Invalid credentials"), "ver la alerta de error")
		x.shot("Alerta de credenciales inválidas", tracereports.Pass)
	})
}

func TestBuscarEmpleadoPorID(t *testing.T) {
	run(t, "OrangeHRM, PIM", "Buscar en PIM un empleado por su Id devuelve ese empleado", func(x *e2e) {
		x.login("Admin", "admin123")
		x.check(x.page.Locator("//span[text()='PIM']").Click(), "abrir PIM")
		fila := x.page.Locator(".oxd-table-body .oxd-table-card").First()
		x.check(expect.Locator(fila).ToBeVisible(), "cargar la lista de empleados")
		id, err := fila.Locator(".oxd-table-cell").Nth(1).InnerText()
		x.check(err, "leer el Id de la primera fila")
		id = strings.TrimSpace(id)
		x.step("Id tomado de la primera fila: " + id)

		x.check(x.page.Locator("//label[text()='Employee Id']/../following-sibling::div//input").Fill(id), "escribir el Id")
		x.check(x.page.Locator("button[type='submit']").Click(), "buscar")
		resultado := x.page.Locator(".oxd-table-body .oxd-table-card").First().Locator(".oxd-table-cell").Nth(1)
		x.check(expect.Locator(resultado).ToHaveText(id), "encontrar el empleado buscado")
		x.shot("Resultado de buscar el Id "+id, tracereports.Pass)
	})
}
