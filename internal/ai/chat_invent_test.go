package ai

import "testing"

// Lo que respondió llama3.1:8b en la prueba local: un XPath inventado para un skeleton que en la página
// se llama skeleton-row. Un selector armado con nombres de la evidencia (o de la conversación) no lo es.
func TestInventsLocator(t *testing.T) {
	known := `Page at the moment of the failure:
- <div class="skeleton-row placeholder-glow"> · XPath: //div[contains(concat(' ',normalize-space(@class),' '),' skeleton-row ')] · CSS: div.skeleton-row
- <button id="guardar-atencion-v2"> Guardar Atención
User: mejora //button[text()='Guardar']`
	cases := []struct {
		answer string
		want   bool
	}{
		{"WebDriverWait(driver, 15).until(EC.invisibility_of_element_located((By.XPATH, \"//div[@class='loading-indicator']\")))", true},
		{"usa `//div[contains(@class,'skeleton-row')]` y espera a que desaparezca", false},
		{"page.locator(\"div.skeleton-row\").wait_for(state=\"hidden\")", false},
		{"driver.find_element(By.CSS_SELECTOR, \".spinner-border\")", true},
		{"//button[contains(translate(normalize-space(.),'ABCDEFGHIJKLMNOPQRSTUVWXYZÁÉÍÓÚ','abcdefghijklmnopqrstuvwxyzaeiou'),'guardar atencion')]", false},
		{"//*[@id='guardar-atencion-v2']", false},
		{"//button[text()='Guardar']", false}, // lo dijo la persona
		{"Abre la pestaña Inspector y prueba el XPath ahí.", false},
	}
	for _, c := range cases {
		if got := inventsLocator(c.answer, known); got != c.want {
			t.Errorf("inventsLocator(%q) = %v, want %v", c.answer, got, c.want)
		}
	}
}

// La prueba real: el modelo repitió su pregunta con una línea pegada al final y el parecido (0,77) no
// llegaba al umbral. Contener casi entera una respuesta anterior también es repetirla.
func TestRepeatsWithSomethingAppended(t *testing.T) {
	asked := "¡Hola! Me alegra ayudarte. Para reproducir el error, necesito saber qué framework estás utilizando para la automatización de pruebas. ¿Es Selenium, Playwright o Cypress?"
	msgs := []ChatMessage{{Role: "user", Text: "dame el xpath"}, {Role: "assistant", Text: asked}, {Role: "user", Text: "es selenium"}}
	if !repeats(asked+"\n\n(No answer yet, waiting for your response)", msgs) {
		t.Fatal("la misma pregunta con una línea pegada es una repetición")
	}
	if repeats("El skeleton es //div[contains(@class,'skeleton-row')]: espera a que desaparezca con WebDriverWait antes del clic.", msgs) {
		t.Fatal("una respuesta nueva no es una repetición")
	}
	long := asked + " Mientras tanto, abre la pestaña Inspector: ahí ves la página del momento del fallo, pruebas el XPath sobre ella y con clic derecho copias un XPath robusto y la espera lista para Selenium o Playwright, sin volver a reproducir el error."
	if repeats(long, msgs) {
		t.Fatal("una respuesta que agrega mucho más no es solo una repetición")
	}
}

func TestToolingFromChat(t *testing.T) {
	user := func(s string) []ChatMessage { return []ChatMessage{{Role: "user", Text: s}} }
	cases := []struct {
		tooling string
		msgs    []ChatMessage
		want    string
	}{
		{"Python", user("es selenium"), "Selenium (Python)"},
		{"", user("uso Playwright"), "Playwright"},
		{"Selenium (Java)", user("con playwright"), "Selenium (Java)"}, // el error ya lo dice
		{"Python", user("¿por qué falló?"), "Python"},
		{"Python", []ChatMessage{{Role: "assistant", Text: "¿Es Selenium?"}}, "Python"}, // lo pregunta el modelo, no lo dice la persona
	}
	for _, c := range cases {
		if got := toolingFromChat(c.tooling, c.msgs); got != c.want {
			t.Errorf("toolingFromChat(%q) = %q, want %q", c.tooling, got, c.want)
		}
	}
}
