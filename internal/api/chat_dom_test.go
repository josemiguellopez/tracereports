package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/ai"
)

func TestChatTooling(t *testing.T) {
	cases := []struct{ fw, msg, want string }{
		{"pytest", "[Retry 1] Error al hacer clic: TimeoutException en (//i[contains(@class,'fa-plus-circle')])[1]", "Selenium (Python)"},
		{"pytest", "TimeoutError: Locator.click: Timeout 5000ms exceeded.\n  - waiting for locator(\"#pay\")", "Playwright (Python)"},
		{"playwright", "locator.click: Timeout 5000ms exceeded", "Playwright (JavaScript/TypeScript)"},
		{"junit5", "org.openqa.selenium.NoSuchElementException: no such element", "Selenium (Java)"},
		{"", "AssertionError: 1 != 2", ""},
	}
	for _, c := range cases {
		if got := chatTooling(c.fw, c.msg, ""); got != c.want {
			t.Errorf("chatTooling(%q, %q) = %q, want %q", c.fw, c.msg, got, c.want)
		}
	}
}

// la página del caso del cliente: el ícono del "+" dentro de un botón, la grilla todavía con su skeleton
// de carga y un backdrop que tapa la pantalla
const clientPage = `{"url":"https://app/agenda","viewport":{"w":1200,"h":800},"page":{"w":1200,"h":800},"elements":[],"nodes":[
{"i":0,"p":-1,"tag":"body","x":0,"y":0,"w":1200,"h":800,"visible":true,"depth":0},
{"i":1,"p":0,"tag":"div","attrs":{"class":"toolbar"},"x":0,"y":0,"w":1200,"h":60,"visible":true,"depth":1},
{"i":2,"p":1,"tag":"button","attrs":{"id":"btn-agregar","class":"btn btn-link"},"x":10,"y":10,"w":40,"h":40,"visible":true,"depth":2},
{"i":3,"p":2,"tag":"i","attrs":{"class":"fa fa-plus-circle"},"x":15,"y":15,"w":20,"h":20,"visible":true,"depth":3},
{"i":4,"p":0,"tag":"div","attrs":{"class":"grid"},"x":0,"y":60,"w":1200,"h":600,"visible":true,"depth":1},
{"i":5,"p":4,"tag":"div","attrs":{"class":"skeleton-row placeholder-glow"},"x":0,"y":60,"w":1200,"h":30,"visible":true,"depth":2},
{"i":6,"p":4,"tag":"div","attrs":{"class":"skeleton-row placeholder-glow"},"x":0,"y":95,"w":1200,"h":30,"visible":true,"depth":2},
{"i":7,"p":0,"tag":"div","attrs":{"class":"modal-backdrop fade show"},"x":0,"y":0,"w":1200,"h":800,"visible":true,"depth":1},
{"i":8,"p":0,"tag":"span","attrs":{},"text":"Cargando agenda…","x":500,"y":400,"w":120,"h":20,"visible":true,"depth":1}
]}`

func TestPageHintsOfTheClientCase(t *testing.T) {
	h := readPageHints(clientPage, "(//i[contains(@class,'fa-plus-circle')])[1]")
	if h == nil {
		t.Fatal("sin hints")
	}
	p := h.prompt()
	for _, want := range []string{
		`<i class="fa fa-plus-circle"> inside <button id="btn-agregar"`, "XPath: //*[@id='btn-agregar']", // lo clickeable alrededor del ícono
		"//div[contains(concat(' ',normalize-space(@class),' '),' skeleton-row ')]", "2 elements, 2 visible", // las filas del skeleton, agrupadas
		"normalize-space()='Cargando agenda…'", // un texto de carga también
		"modal-backdrop", "intercept the click",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("falta %q en:\n%s", want, p)
		}
	}
	fb := h.fallback("es", "Selenium (Python)")
	if !strings.Contains(fb, `WebDriverWait(driver, 15).until(EC.invisibility_of_element_located((By.XPATH, "//div[contains(concat(' ',normalize-space(@class),' '),' skeleton-row ')]")))`) ||
		!strings.Contains(fb, "Inspector") {
		t.Fatalf("respaldo:\n%s", fb)
	}
	if fb := h.fallback("en", "Selenium (Java)"); !strings.Contains(fb, "ExpectedConditions.invisibilityOfElementLocated(By.xpath(") {
		t.Fatalf("respaldo en Java:\n%s", fb)
	}
}

func TestPageHintsWithoutTheElementOrLoaders(t *testing.T) {
	page := `{"url":"https://app/","nodes":[{"i":0,"p":-1,"tag":"body","visible":true,"w":10,"h":10},{"i":1,"p":0,"tag":"button","attrs":{"id":"ok"},"text":"OK","visible":true,"w":5,"h":5}]}`
	p := readPageHints(page, "#guardar").prompt()
	if !strings.Contains(p, "Nothing in the page matched it") || !strings.Contains(p, "No loading indicator") {
		t.Fatal(p)
	}
	if readPageHints(`{"elements":[{"tag":"button"}]}`, "#x") != nil {
		t.Fatal("un snapshot básico (sin nodes) no da hints")
	}
}

// El caso que mandó el cliente: el modelo pregunta el framework una y otra vez. Ahora el framework va en
// la evidencia, el DOM del fallo también, y si el modelo repite su respuesta igual se contesta con el
// skeleton y la espera armados desde el snapshot.
func TestBuddyChatDoesNotLoopAndUsesTheFailurePage(t *testing.T) {
	clearAIEnv(t)
	srv, testID := newTestServer(t)
	asked := "¡Hola! Me alegra ayudarte. Para reproducir el error, necesito saber qué framework estás utilizando para la automatización de pruebas. ¿Es Selenium, Playwright o Cypress?"
	mock, prompts := fakeOllama(t, asked)
	if err := srv.AI.SetConfig(ai.Config{Provider: "ollama", BaseURL: mock.URL, Model: "llama3.1:8b"}); err != nil {
		t.Fatal(err)
	}
	test, _ := srv.Store.GetTest(testID)
	finish := fmt.Sprintf("/api/v1/tests/%d/finish", testID)
	if rec := call(t, srv, "PATCH", finish, `{"status":"FAIL","error_message":"[Retry 1] Error al hacer clic: TimeoutException en (//i[contains(@class,'fa-plus-circle')])[1]"}`); rec.Code != http.StatusOK {
		t.Fatalf("finish: %d %s", rec.Code, rec.Body)
	}
	if err := srv.Store.SaveDOM(testID, clientPage); err != nil {
		t.Fatal(err)
	}
	body := chatBody(test.RunID, testID,
		"necesito el xpath o id de ese efecto esqueleton de espera para agregar un wait", asked, "es selenium")
	rec := call(t, srv, "POST", "/api/v1/ui/ai/chat", body)
	var res struct{ Answer string }
	if json.Unmarshal(rec.Body.Bytes(), &res); rec.Code != http.StatusOK {
		t.Fatalf("chat: %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(res.Answer, "framework") || !strings.Contains(res.Answer, "skeleton-row") || !strings.Contains(res.Answer, "invisibility_of_element_located") {
		t.Fatalf("respuesta: %s", res.Answer)
	}
	var p []string // las del chat (el FAIL también pide su diagnóstico al mismo modelo)
	for _, x := range prompts() {
		if strings.Contains(x, "You are Trace Buddy") {
			p = append(p, x)
		}
	}
	if len(p) != 2 {
		t.Fatalf("una pregunta y un reintento al ver la repetición, no %d", len(p))
	}
	// la ejecución de prueba no dice su framework: el error basta para saber que es Selenium
	for _, want := range []string{"The test uses Selenium (detected", "skeleton-row", "btn-agregar", "Inspector tab"} {
		if !strings.Contains(p[0], want) {
			t.Errorf("la evidencia no lleva %q", want)
		}
	}
	if !strings.Contains(p[1], "repeated an earlier answer") {
		t.Error("el reintento no dice que la respuesta se repitió")
	}
}

// Una respuesta nueva no se reintenta ni se reemplaza.
func TestBuddyChatKeepsAnAnswerThatMovesForward(t *testing.T) {
	clearAIEnv(t)
	srv, testID := newTestServer(t)
	mock, prompts := fakeOllama(t, "Espera al skeleton: WebDriverWait(driver, 15).until(EC.invisibility_of_element_located((By.CSS_SELECTOR, \"div.skeleton-row\")))")
	srv.AI.SetConfig(ai.Config{Provider: "ollama", BaseURL: mock.URL, Model: "llama3.1:8b"})
	test, _ := srv.Store.GetTest(testID)
	srv.Store.SaveDOM(testID, clientPage)
	rec := call(t, srv, "POST", "/api/v1/ui/ai/chat", chatBody(test.RunID, testID, "¿cómo espero el skeleton?", "¿Usas Selenium?", "sí"))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "div.skeleton-row") || len(prompts()) != 1 {
		t.Fatalf("%d %s (%d llamadas)", rec.Code, rec.Body, len(prompts()))
	}
}

// Un test sin página del fallo (terminó en WARNING, o sigue corriendo): el chat usa la última página que
// guardó, p. ej. la del WARNING con la grilla todavía en skeleton.
func TestBuddyChatUsesTheLastStepPageWithoutAFailure(t *testing.T) {
	clearAIEnv(t)
	srv, testID := newTestServer(t)
	mock, prompts := fakeOllama(t, "Espera a que desaparezca el skeleton.")
	srv.AI.SetConfig(ai.Config{Provider: "ollama", BaseURL: mock.URL, Model: "llama3.1:8b"})
	test, _ := srv.Store.GetTest(testID)
	if _, err := srv.Store.SaveDOMStepKind(testID, "warning", "La grilla sigue cargando", 0, clientPage); err != nil {
		t.Fatal(err)
	}
	rec := call(t, srv, "POST", "/api/v1/ui/ai/chat", chatBody(test.RunID, testID, "¿cuál es el xpath del skeleton?"))
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if p := prompts(); len(p) != 1 || !strings.Contains(p[0], "skeleton-row") {
		t.Fatal("la evidencia no lleva la página del WARNING")
	}
}

// La prueba real con llama3.1:8b: el modelo inventó "//div[@class='loading-indicator']". El chat toma la
// página del WARNING (la del final ya cargó y no tiene el skeleton), ve que el XPath no sale de la página,
// reintenta una vez y responde con el skeleton y la espera armados desde el snapshot.
func TestBuddyChatReplacesAnInventedLocator(t *testing.T) {
	clearAIEnv(t)
	srv, testID := newTestServer(t)
	invented := "Usa //div[@class='loading-indicator'] con WebDriverWait."
	mock, prompts := fakeOllama(t, invented)
	srv.AI.SetConfig(ai.Config{Provider: "ollama", BaseURL: mock.URL, Model: "llama3.1:8b"})
	test, _ := srv.Store.GetTest(testID)
	srv.Store.SaveDOMStepKind(testID, "warning", "La grilla sigue cargando", 0, clientPage)
	srv.Store.SaveDOMStepKind(testID, "step", "Agenda cargada", 0, `{"url":"https://app/agenda","nodes":[{"i":0,"p":-1,"tag":"body","visible":true,"w":10,"h":10}]}`)
	rec := call(t, srv, "POST", "/api/v1/ui/ai/chat", chatBody(test.RunID, testID, "necesito el xpath del esqueleton para agregar un wait"))
	var res struct{ Answer string }
	if json.Unmarshal(rec.Body.Bytes(), &res); rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if strings.Contains(res.Answer, "loading-indicator") || !strings.Contains(res.Answer, "skeleton-row") || !strings.Contains(res.Answer, "invisibility_of_element_located") {
		t.Fatalf("respuesta: %s", res.Answer)
	}
	if p := prompts(); len(p) != 2 || !strings.Contains(p[1], "names that are not in the page") {
		t.Fatalf("una pregunta y un reintento que explica por qué: %d", len(p))
	}
}
