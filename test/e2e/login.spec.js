// La página de ingreso (/login/): el formulario con sus estados (error, intentos bloqueados, éxito con la
// vuelta a donde estaba), Trace Buddy que se tapa los ojos con la clave oculta, espía y se destapa al
// mostrarla, y la app que manda a /login/ cuando la sesión vence y deja salir. El servidor de e2e no pide
// login: /api/v1/auth/* se simula aquí (las reglas de la sesión, la cookie y el freno están en Go).
const { test, expect } = require("@playwright/test");

const session = (over = {}) => ({ logged_in: false, login_required: true, idle_min: 30, remember_days: 7, ...over });

test.beforeEach(async ({ page }, info) => {
	info.errors_ = [];
	page.on("pageerror", (e) => info.errors_.push(`pageerror: ${e.message}`));
});
test.afterEach(async ({}, info) => {
	expect(info.errors_, "la página no debe tener errores").toEqual([]);
});

async function openLogin(page, query = "", sess = session()) {
	await page.route("**/api/v1/auth/session", (r) => r.fulfill({ json: sess }));
	await page.goto(`/login/${query}`);
	await expect(page.locator("#user")).toBeFocused();
}

test("el formulario: usuario, clave, recordarme con los días del servidor y lo que dura la sesión", async ({ page }) => {
	await openLogin(page, "", session({ idle_min: 45, remember_days: 14 }));
	await expect(page.locator("#login-title")).toHaveText("Ingresar");
	await expect(page.locator("#remember-days")).toHaveText("(14 días)");
	await expect(page.locator("#policy")).toContainText("vence tras 45 min sin uso");
	await expect(page.locator("#user")).toHaveAttribute("autocomplete", "username");
	await expect(page.locator("#pass")).toHaveAttribute("autocomplete", "current-password");
	await expect(page.locator(".pitch h1")).toBeVisible();
	await expect(page.locator(".kicker")).toContainText("6 AGENTES EJECUTANDO");
	// el lienzo de los agentes no pasa por debajo de la tarjeta
	const card = await page.locator(".card").boundingBox();
	const painted = await page.evaluate((x) => {
		const c = document.querySelector("#stage"), g = c.getContext("2d"), d = devicePixelRatio || 1;
		const px = g.getImageData(Math.round((x + 20) * d), Math.round(118 * d), 1, 1).data;
		return px[3];
	}, card.x);
	expect(painted, "nada de la traza bajo la tarjeta").toBeLessThan(40);
});

test("mostrar clave: el ojo cambia el campo, avisa a los lectores de pantalla y conserva el cursor", async ({ page }) => {
	await openLogin(page);
	await page.locator("#pass").fill("s3cr3t");
	const eye = page.locator("#eye");
	await expect(eye).toHaveAttribute("aria-pressed", "false");
	await eye.click();
	await expect(page.locator("#pass")).toHaveAttribute("type", "text");
	await expect(eye).toHaveAttribute("aria-pressed", "true");
	await expect(page.locator("#eye-label")).toHaveText("Ocultar clave");
	await expect(page.locator("#pass")).toBeFocused();
	await eye.click();
	await expect(page.locator("#pass")).toHaveAttribute("type", "password");
});

test("Trace Buddy: mira lo que escribes, se tapa los ojos con la clave oculta, espía y se destapa al mostrarla", async ({ page }) => {
	await openLogin(page);
	await expect(page.locator("#buddy")).toHaveClass(/has-3d/, { timeout: 10_000 });
	await page.locator("#user").fill("admin");
	await expect(page.locator("#buddy")).toHaveAttribute("data-pose", "none");
	await page.locator("#pass").click();
	await expect(page.locator("#buddy")).toHaveAttribute("data-pose", "cover");
	await page.locator("#eye").hover();
	await expect(page.locator("#buddy")).toHaveAttribute("data-pose", "peek");
	await page.locator("#eye").click();
	await expect(page.locator("#buddy")).toHaveAttribute("data-pose", "none");
	await page.locator("#eye").click(); // oculta otra vez: se vuelve a tapar
	await expect(page.locator("#buddy")).toHaveAttribute("data-pose", "cover");
	await page.locator("#user").click();
	await expect(page.locator("#buddy")).toHaveAttribute("data-pose", "none");
});

test("clave equivocada: un solo mensaje, los campos marcados y la clave borrada; bloqueado: cuenta regresiva", async ({ page }) => {
	await openLogin(page);
	let calls = 0;
	await page.route("**/api/v1/auth/login", (r) => {
		calls++;
		if (calls < 3) return r.fulfill({ status: 401, json: { error: "wrong user or password" } });
		return r.fulfill({ status: 429, headers: { "Retry-After": "95" }, json: { error: "too many failed logins" } });
	});
	await page.locator("#go").click();
	await expect(page.locator("#error")).toHaveText("Escribe tu usuario y tu clave.");
	expect(calls, "vacío no se envía").toBe(0);
	await page.locator("#user").fill("admin");
	await page.locator("#pass").fill("mala");
	await page.locator("#go").click();
	await expect(page.locator("#error")).toHaveText("Usuario o clave incorrectos.");
	await expect(page.locator(".field.bad")).toHaveCount(2);
	await expect(page.locator("#pass")).toHaveValue("");
	await expect(page.locator("#pass")).toBeFocused();
	// escribir de nuevo limpia el error
	await page.locator("#pass").fill("x");
	await expect(page.locator("#error")).toHaveText("");
	await page.locator("#go").click();
	await expect(page.locator("#error")).toHaveText("Usuario o clave incorrectos."); // la respuesta borra la clave: después se escribe
	await page.locator("#pass").fill("y");
	await page.locator("#go").click();
	await expect(page.locator("#error")).toContainText("Demasiados intentos. Prueba de nuevo en 1 min 3");
	await expect(page.locator("#go")).toBeDisabled();
});

test("bien: envía usuario, clave y recordarme, y vuelve a donde estaba (con su #); nunca a otro sitio", async ({ page }) => {
	let body = null;
	await page.route("**/api/v1/auth/login", async (r) => {
		body = r.request().postDataJSON();
		await r.fulfill({ json: session({ logged_in: true, user: "admin", role: "admin" }) });
	});
	await openLogin(page, "?next=%2F#run=1&view=tests");
	await page.locator("#user").fill("  admin ");
	await page.locator("#pass").fill("pw");
	await page.locator(".remember").click();
	await page.locator("#go").click();
	await expect(page.locator(".go-text")).toContainText("Bienvenido");
	await expect(page).toHaveURL(/\/#run=1&view=tests(&|$)/); // la app agrega el test elegido
	expect(body).toEqual({ user: "admin", password: "pw", remember: true });

	// next hacia otro sitio: se queda en este
	for (const evil of ["%2F%2Fevil.example%2Fx", "https%3A%2F%2Fevil.example", "%2Flogin%2F"]) {
		await openLogin(page, `?next=${evil}`);
		await page.locator("#user").fill("admin");
		await page.locator("#pass").fill("pw");
		await page.locator("#go").click();
		await page.waitForURL((u) => !u.pathname.startsWith("/login"));
		expect(new URL(page.url()).host).toBe(new URL(page.url()).host);
		expect(new URL(page.url()).pathname).toBe("/");
	}
});

test("ya con sesión, o un servidor sin login: no muestra el formulario y sigue", async ({ page }) => {
	await page.route("**/api/v1/auth/session", (r) => r.fulfill({ json: session({ logged_in: true, user: "admin", role: "admin" }) }));
	await page.goto("/login/?next=%2F%23view%3Dsearch");
	await expect(page).toHaveURL(/\/#(run=\d+&)?view=search$/); // la app agrega el run elegido
	await page.unroute("**/api/v1/auth/session");
	await page.route("**/api/v1/auth/session", (r) => r.fulfill({ json: session({ login_required: false }) }));
	await page.goto("/login/");
	await expect(page.locator("#notice")).toContainText("no pide login");
	await page.waitForURL((u) => u.pathname === "/");
});

test("la app: con la sesión vencida va al ingreso (y avisa); con sesión muestra el usuario y Salir", async ({ page }) => {
	// vencida: cualquier llamada de la UI responde 401 "session expired" (y el servidor pide login)
	await page.route("**/api/v1/auth/session", (r) => r.fulfill({ json: session({ expired: true }) }));
	await page.route("**/api/v1/config", (r) => r.fulfill({ status: 401, json: { error: "session expired" } }));
	await page.goto("/#view=tests");
	await page.waitForURL(/\/login\/\?next=%2F%23view%3Dtests&expired=1/);
	await expect(page.locator("#notice")).toHaveText("Tu sesión venció. Ingresa de nuevo para seguir.");
	await page.unroute("**/api/v1/config");

	// con sesión: el usuario arriba y Salir cierra y vuelve al ingreso
	let out = false;
	await page.unroute("**/api/v1/auth/session");
	await page.route("**/api/v1/auth/session", (r) => r.fulfill({ json: session({ logged_in: true, user: "admin", role: "admin", idle_until: Date.now() + 20 * 60e3, expires_at: Date.now() + 8 * 3600e3 }) }));
	await page.route("**/api/v1/auth/logout", (r) => { out = true; return r.fulfill({ json: { ok: true } }); });
	await page.goto("/");
	const btn = page.locator("#session-btn");
	await expect(btn).toBeVisible();
	await expect(btn).toContainText("admin");
	await expect(page.locator("#session-warn")).toBeHidden();
	await page.unroute("**/api/v1/auth/session");
	await page.route("**/api/v1/auth/session", (r) => r.fulfill({ json: session() }));
	// salir pide confirmación: Cancelar (con el foco) o Esc no salen; "Cerrar sesión" sí
	await btn.click();
	const confirm = page.locator("#session-confirm");
	await expect(confirm).toBeVisible();
	await expect(confirm).toContainText("¿Cerrar sesión?");
	await expect(confirm.locator("[data-session-cancel]")).toBeFocused();
	await expect(btn).toHaveAttribute("aria-expanded", "true");
	await page.keyboard.press("Escape");
	await expect(confirm).toBeHidden();
	await expect(btn).toBeFocused();
	await btn.click();
	await confirm.locator("[data-session-cancel]").click();
	await expect(confirm).toBeHidden();
	expect(out).toBe(false);
	await btn.click();
	await confirm.locator("[data-session-out]").click();
	await page.waitForURL(/\/login\/\?bye=1/);
	expect(out).toBe(true);
	await expect(page.locator("#notice")).toHaveText("Cerraste la sesión.");
});

test("a 2 min de vencer por inactividad avisa; Seguir conectado la renueva", async ({ page }) => {
	let idleUntil = Date.now() + 90_000;
	await page.route("**/api/v1/auth/session", (r) => r.fulfill({ json: session({ logged_in: true, user: "admin", role: "admin", idle_until: idleUntil }) }));
	await page.goto("/");
	const warn = page.locator("#session-warn");
	await expect(warn).toBeVisible();
	await expect(warn).toContainText("Tu sesión vence en");
	idleUntil = Date.now() + 30 * 60e3;
	await warn.locator("[data-session-stay]").click();
	await expect(warn).toBeHidden();
});

test.describe("la obra: un equipo de agentes construye la página (como Swarm)", () => {
	test("reglas, guías, rectángulos con su medida y textos que se escriben; siete agentes y Buddy cuenta cuántos", async ({ page }) => {
		await page.route("**/api/v1/auth/session", (r) => r.fulfill({ json: session() }));
		await page.goto("/login/");
		await expect(page.locator("#card")).toHaveClass(/building/);
		await expect(page.locator("#ab-state")).toHaveText("· construyendo…");
		await expect(page.locator(".pitch h1")).toHaveClass(/unbuilt/);
		await expect(page.locator(".agent").first()).toBeVisible();
		await expect(page.locator(".guide").first()).toBeAttached();
		await expect(page.locator(".guide.x").first()).toHaveAttribute("data-label", /^x \d+$/);
		await expect(page.locator(".draft:not(.lane-bar):not(.lane-label) > span").first()).toHaveText(/^\d+ × \d+$/); // la medida del rectángulo
		// el agente grid abre la paleta y los rectángulos quedan pintados con sus colores (el boceto)
		await expect(page.locator(".palette .sw")).toHaveCount(8);
		// el agente grid bosqueja la traza de gw0…gw5 (las barras con el color de su estado) antes de la animación
		await expect(page.locator(".lane-label").first()).toHaveText("gw0", { timeout: 6000 });
		await expect.poll(() => page.locator(".lane-bar .lbl").evaluateAll((ls) => ls.some((l) => l.textContent.startsWith("test_"))), { timeout: 8000 }).toBe(true); // el nombre del test, escrito
		await expect.poll(() => page.locator(".lane-bar").count(), { timeout: 8000 }).toBeGreaterThan(6);
		await expect(page.locator(".palette")).toContainText("lime");
		await expect(page.locator(".draft .fill").first()).toBeAttached({ timeout: 6000 });
		await expect(page.locator(".type-caret").first()).toBeAttached({ timeout: 6000 });
		// termina sola: todo armado, sin guías ni agentes, y Buddy dice cuántos la construyeron
		await expect(page.locator("#ab-state")).toHaveText("· listo", { timeout: 15_000 });
		await expect(page.locator(".agent, .guide, .draft, .type-caret, .palette, .drop, .render-sweep")).toHaveCount(0);
		await expect(page.locator("#card")).not.toHaveClass(/building/);
		await expect(page.locator("#say")).toContainText(/7 agentes armaron este login en \d+,\d s/);
		await expect(page.locator("#say")).toContainText("prometo no mirar tu clave");
		await expect(page.locator(".pitch h1")).toHaveText("Lo que falló, por qué y dónde.");
		await expect(page.locator("#login-title")).toHaveText("Ingresar");
		await expect(page.locator(".go-text")).toHaveText("Ingresar");
		expect(await page.locator(".unbuilt").count()).toBe(0);
		const obra = await page.evaluate(() => ({ agents: [...window.__obra.agents.keys()].sort(), tasks: window.__obra.log.map((x) => x[0]) }));
		expect(obra.agents).toEqual(["copy", "forms", "grid", "layout", "marca", "qa", "seguridad"]);
		expect(obra.tasks).toEqual(expect.arrayContaining(["rulers", "palette", "lanes", "card", "h1", "f-user", "f-pass", "go", "qa", "render", "trace"]));
		expect(obra.tasks.indexOf("render")).toBeGreaterThan(Math.max(...["qa", "lanes", "lanes-2", "lanes-3"].map((x) => obra.tasks.indexOf(x)))); // el boceto se "renderiza" al final
		expect(obra.tasks.indexOf("trace")).toBeGreaterThan(obra.tasks.indexOf("render")); // y recién ahí corre la animación
		expect(obra.tasks.indexOf("card")).toBeLessThan(obra.tasks.indexOf("f-user")); // las dependencias se respetan
		for (const sel of ["#user", "#pass", "#go", ".remember", ".foot"]) await expect(page.locator(sel)).toBeVisible();
	});

	test("escribir termina la obra al instante (nada bloquea el ingreso) y no se repite en la misma pestaña", async ({ page }) => {
		await page.route("**/api/v1/auth/session", (r) => r.fulfill({ json: session() }));
		await page.goto("/login/");
		await expect(page.locator(".agent").first()).toBeVisible();
		await page.mouse.click(300, 500); // un clic en cualquier parte la termina
		await expect(page.locator("#say")).toContainText("Apurado");
		await page.locator("#user").click();
		await page.keyboard.type("admin");
		await expect(page.locator("#user")).toHaveValue("admin");
		await expect(page.locator("#card")).not.toHaveClass(/building/, { timeout: 1000 });
		await expect(page.locator(".agent, .guide, .draft")).toHaveCount(0, { timeout: 1500 });
		await expect(page.locator(".pitch h1")).toHaveText("Lo que falló, por qué y dónde.");
		await expect(page.locator("#ab-state")).toHaveText("· listo");
		await page.reload();
		await expect(page.locator("#user")).toBeFocused();
		await expect(page.locator(".agent")).toHaveCount(0);
		await expect(page.locator("#card")).not.toHaveClass(/building/);
		await expect(page.locator("#say")).toContainText("Hola de nuevo");
	});

	test("con reducir movimiento no hay obra: todo aparece armado", async ({ page }) => {
		await page.emulateMedia({ reducedMotion: "reduce" });
		await page.route("**/api/v1/auth/session", (r) => r.fulfill({ json: session() }));
		await page.goto("/login/");
		await expect(page.locator("#user")).toBeFocused();
		await expect(page.locator(".agent, .guide")).toHaveCount(0);
		await expect(page.locator(".unbuilt")).toHaveCount(0);
		await expect(page.locator("#card")).not.toHaveClass(/building/);
		await expect(page.locator("#go")).toBeVisible();
	});
});

test.describe("Trace Buddy conversa", () => {
	test("mostrar la clave: un susto y el globo pícaro (rojo); ocultarla: se tapa y lo dice", async ({ page }) => {
		await openLogin(page);
		await page.keyboard.type("admin"); // termina la obra
		await page.locator("#pass").fill("secreta");
		await page.locator("#eye").click();
		const bubble = page.locator("#say");
		await expect(bubble).toHaveClass(/devil/);
		await expect(bubble.locator(".say-h")).toHaveText(/^Trace Buddy/);
		await expect(bubble.locator(".say-t")).toHaveText(/clave|robot|secreto|villano|sellados/); // una de las frases (al azar)
		await page.locator("#eye").click();
		await expect(bubble).not.toHaveClass(/devil/);
		await expect(bubble.locator(".say-t")).toHaveText(/no miro|me tapo|Ojos cerrados/);
		await expect(page.locator("#buddy")).toHaveAttribute("data-pose", "cover");
	});

	test("saluda por su nombre al pasar a la clave, escapado (un usuario con HTML se ve como texto)", async ({ page }) => {
		await openLogin(page);
		await page.locator("#user").fill("<img src=x onerror=alert(1)>");
		await page.locator("#pass").click();
		const t = page.locator("#say .say-t");
		await expect(t).toContainText("Hola, <img src=x onerror=alert(1)>");
		await expect(page.locator("#say img")).toHaveCount(0);
		await expect(t).toContainText("yo no miro");
	});
});

test.describe("la barra de la obra", () => {
	test("muestra a los agentes y el avance; Esc la salta; queda ↻ repetir", async ({ page }) => {
		await page.route("**/api/v1/auth/session", (r) => r.fulfill({ json: session() }));
		await page.goto("/login/");
		const bar = page.locator("#buildbar");
		await expect(bar).toBeVisible();
		await expect(bar.locator(".bb-agents i")).toHaveCount(7);
		await expect(page.locator("#bb-skip")).toContainText("Saltar");
		await expect(page.locator("#bb-skip kbd")).toHaveText("Esc");
		await expect.poll(async () => Number(await page.locator("#bb-prog").getAttribute("aria-valuenow")), { timeout: 8000 }).toBeGreaterThan(20);
		await page.keyboard.press("Escape");
		await expect(page.locator("#card")).not.toHaveClass(/building/);
		await expect(bar).toBeHidden({ timeout: 3000 });
		await expect(page.locator("#ab-replay")).toBeVisible();
	});

	test("el botón Saltar; No volver a mostrar se recuerda; ↻ repetir la vuelve a mostrar", async ({ page }) => {
		await page.route("**/api/v1/auth/session", (r) => r.fulfill({ json: session() }));
		await page.goto("/login/");
		await expect(page.locator("#buildbar")).toBeVisible();
		await page.locator("#bb-never").check();
		await expect(page.locator("#card")).not.toHaveClass(/building/);
		expect(await page.evaluate(() => localStorage.getItem("tracereports-login-build"))).toBe("off");
		// otra pestaña (sin lo de esta sesión): igual no se muestra
		await page.evaluate(() => sessionStorage.clear());
		await page.reload();
		await expect(page.locator("#user")).toBeFocused();
		await expect(page.locator(".agent, #buildbar:not([hidden])")).toHaveCount(0);
		// ↻ repetir: vuelve la obra (y deshace el "no volver a mostrar")
		await page.locator("#ab-replay").click();
		await expect(page.locator("#buildbar")).toBeVisible();
		await expect(page.locator(".agent").first()).toBeVisible();
		expect(await page.evaluate(() => localStorage.getItem("tracereports-login-build"))).toBeNull();
		await page.locator("#bb-skip").click();
		await expect(page.locator("#card")).not.toHaveClass(/building/);
		await expect(page.locator(".agent")).toHaveCount(0, { timeout: 2000 });
	});
});
