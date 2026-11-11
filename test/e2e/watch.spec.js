// Mientras alguien mira un reporte, otra persona lanza una ejecución: lo que se está viendo no
// cambia solo (ni el selector); un aviso ofrece verla en vivo. Va en su propio archivo, al final:
// crea ejecuciones nuevas y las demás pruebas cuentan con las del seed.
const { test, expect } = require("@playwright/test");

const seed = () => JSON.parse(process.env.E2E_SEED);
const created = [];

async function newRun(request, name, execution_id = "") {
	const res = await request.post("/api/v1/runs", { data: { name, environment: "qa", project: "e2e", branch: "dev", execution_id } });
	expect(res.ok()).toBeTruthy();
	const { run_id: id } = await res.json();
	created.push(id);
	return id;
}

async function viewSeedRun(page) {
	await page.goto(`/#run=${seed().run}&view=tests`);
	await expect(page.locator("#report-name")).toHaveText("E2E actual");
	await expect(page.locator("#test-collection .collection-item")).toHaveCount(3);
}

test.afterAll(async ({ request }) => {
	for (const id of created) await request.patch(`/api/v1/runs/${id}/finish`, { data: {} });
});

test("otra ejecución empieza: no cambia la que se ve y avisa para verla en vivo", async ({ page, request }) => {
	const { run } = seed();
	await viewSeedRun(page);
	const live = await newRun(request, "E2E en vivo");

	const toast = page.locator("#run-toast");
	await expect(toast).toBeVisible();
	await expect(toast).toContainText("Empezó otra ejecución");
	await expect(toast).toContainText(`#${live} · E2E en vivo · qa · dev`);
	// lo que se estaba viendo sigue igual, también el selector
	await expect(page.locator("#run-select")).toHaveValue(String(run));
	await expect(page.locator("#report-name")).toHaveText("E2E actual");
	await expect(page.locator(`#run-select option[value="${live}"]`)).toHaveCount(1);
	// también se nota con la pestaña en segundo plano
	await expect(page).toHaveTitle(/^● /);
	await expect(page.locator("#new-run-badge")).toBeVisible();

	await toast.locator("[data-toast-open]").click();
	await expect(page.locator("#report-name")).toHaveText("E2E en vivo");
	await expect(page.locator("#run-select")).toHaveValue(String(live));
	await expect(page.locator("#live-pill")).toBeVisible();
	await expect(toast).toBeHidden();
	await expect(page.locator("#new-run-badge")).toBeHidden();
	await expect(page).not.toHaveTitle(/^● /);
	expect(page.url()).toContain(`run=${live}`);
});

test("'Ahora no' deja la insignia junto al selector, y la insignia lleva a la ejecución", async ({ page, request }) => {
	await viewSeedRun(page);
	const live = await newRun(request, "E2E más tarde");
	const toast = page.locator("#run-toast");
	await toast.locator("[data-toast-close]").click();
	await expect(toast).toBeHidden();
	const badge = page.locator("#new-run-badge");
	await expect(badge).toBeVisible();
	await expect(badge).toHaveText("1 nueva");
	await expect(page.locator("#report-name")).toHaveText("E2E actual");

	await badge.click();
	await expect(page.locator("#report-name")).toHaveText("E2E más tarde");
	await expect(page.locator("#run-select")).toHaveValue(String(live));
	await expect(badge).toBeHidden();
});

test("varias ejecuciones nuevas: 'Ahora no' calla esa, la siguiente vuelve a avisar y la insignia cuenta las dos", async ({ page, request }) => {
	await viewSeedRun(page);
	const toast = page.locator("#run-toast");
	await newRun(request, "E2E shard 1");
	await expect(toast).toContainText("E2E shard 1");
	await toast.locator("[data-toast-close]").click();
	const second = await newRun(request, "E2E shard 2");
	await expect(toast).toBeVisible(); // otra nueva: avisa de nuevo aunque se haya cerrado la anterior
	await expect(toast).toContainText("Empezó otra ejecución"); // la primera quedó silenciada: el aviso es de la nueva
	await expect(toast).toContainText(`#${second} · E2E shard 2`);
	await expect(page.locator("#new-run-badge")).toHaveText("2 nuevas");
	// cambiar con el selector también cuenta como vista
	await page.locator("#run-select").selectOption(String(second), { force: true });
	await expect(page.locator("#report-name")).toHaveText("E2E shard 2");
	await expect(page.locator("#new-run-badge")).toHaveText("1 nueva");
});

test("un lanzador con un run por archivo es UNA ejecución: un aviso con su cuenta, y 'Ahora no' la calla", async ({ page, request }) => {
	await viewSeedRun(page);
	const toast = page.locator("#run-toast");
	const exe = `e2e-suite-${Date.now()}`;
	await newRun(request, "shop/test_a.py", exe);
	await expect(toast).toContainText("Empezó otra ejecución");
	await newRun(request, "shop/test_b.py", exe);
	await newRun(request, "shop/test_c.py", exe);
	await expect(toast).toContainText("Empezó una ejecución · 3 corridas");
	await expect(toast).toContainText(exe);
	await expect(page.locator("#new-run-badge")).toHaveText("1 nueva"); // una ejecución, no tres
	await toast.locator("[data-toast-close]").click();
	await expect(toast).toBeHidden();
	await newRun(request, "shop/test_d.py", exe); // otra corrida de la misma: ya no abre el aviso
	await page.waitForTimeout(1500);
	await expect(toast).toBeHidden();
	await expect(page.locator("#new-run-badge")).toHaveText("1 nueva");
	// la campana la lista para reactivarla
	await page.locator("#notify-bell").click();
	const panel = page.locator("#notify-panel");
	await expect(panel).toBeVisible();
	await expect(panel.locator(".np-muted li")).toContainText(exe);
	await panel.locator("[data-notify-unmute]").click();
	await expect(panel.locator(".np-muted")).toHaveCount(0);
	await expect(toast).toBeVisible(); // reactivada: vuelve a avisar lo pendiente
	// "Ver en vivo" abre En vivo de esa ejecución
	await page.keyboard.press("Escape");
	await toast.locator("[data-toast-open]").click();
	await expect(page).toHaveURL(new RegExp(`view=live&exec=x%3A${exe}`));
	await expect(page.locator("#new-run-badge")).toBeHidden();
});

test("'Silenciar 15 min': deshacer, la campana dice cuánto queda, nada avisa y 'Reactivar ahora' lo vuelve a encender", async ({ page, request }) => {
	await viewSeedRun(page);
	const toast = page.locator("#run-toast"), bell = page.locator("#notify-bell");
	await expect(bell).toBeVisible();
	await expect(bell).toHaveAttribute("aria-label", "Avisos de ejecuciones nuevas activos");
	await newRun(request, "E2E ruidosa 1");
	await toast.locator("[data-toast-snooze]").click();
	await expect(toast).toContainText("Avisos silenciados por 15 min");
	await toast.locator("[data-toast-undo]").click(); // deshacer
	await expect(bell).not.toHaveClass(/off/);
	await expect(toast).toContainText("E2E ruidosa 1");
	await toast.locator("[data-toast-snooze]").click();
	await expect(bell).toHaveClass(/off/);
	await expect(bell).toContainText("15 min");
	await expect(bell).toHaveAttribute("aria-label", /silenciados: quedan 15 min/);
	await expect(toast).toBeHidden({ timeout: 10000 }); // el "Deshacer" se va solo
	await newRun(request, "E2E ruidosa 2");
	await expect(page.locator("#new-run-badge")).toHaveText("2 nuevas"); // la insignia cuenta igual
	await expect(toast).toBeHidden();
	// lo silenciado sobrevive a recargar la página (queda en este navegador)
	await page.reload();
	await expect(page.locator("#notify-bell")).toHaveClass(/off/);
	await page.locator("#notify-bell").click();
	const panel = page.locator("#notify-panel");
	await expect(panel.locator(".np-state")).toContainText(/Silenciados hasta las \d{2}:\d{2} \(quedan 15 min\)/);
	await panel.locator("[data-notify-resume]").click();
	await expect(page.locator("#notify-bell")).not.toHaveClass(/off/);
	await expect(panel.locator(".np-state")).toContainText("Activos");
	await page.mouse.click(5, 300); // clic afuera: se cierra
	await expect(panel).toBeHidden();
	await newRun(request, "E2E ruidosa 3");
	await expect(toast).toContainText("E2E ruidosa 3");
});
