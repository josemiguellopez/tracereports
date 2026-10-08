const http = require("node:http");
const { test, expect } = require("@playwright/test");

async function provider(holdFirst = false) {
	let calls = 0, started, release;
	const first = new Promise((resolve) => { started = resolve; });
	const gate = new Promise((resolve) => { release = resolve; });
	const server = http.createServer((req, res) => {
		let body = "";
		req.on("data", (chunk) => { body += chunk; });
		req.on("end", async () => {
			const escalation = body.includes("senior QA lead writing an escalation");
			if (escalation && ++calls === 1) { started(); if (holdFirst) await gate; }
			const marker = body.includes("NEW_EVIDENCE") ? "NEW_EVIDENCE_SUMMARY" : "OLD_EVIDENCE_SUMMARY";
			const result = { title: marker, severity: "medium", headline: marker, what_happened: marker,
				impact: "fake impact", evidence: [marker], root_cause: "fake cause", next_steps: ["inspect"], owner: "QA",
				category: "LOGIC_BUG", summary: marker, suggestion: "inspect", incidents: [] };
			res.setHeader("Content-Type", "application/json");
			res.end(JSON.stringify({ choices: [{ message: { content: JSON.stringify(result) } }] }));
		});
	});
	await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
	return { url: `http://127.0.0.1:${server.address().port}`, first, release,
		calls: () => calls, close: async () => { release(); await new Promise((resolve) => server.close(resolve)); } };
}

async function json(request, method, url, data) {
	const res = await request.fetch(url, { method, data });
	expect(res.ok(), await res.text()).toBeTruthy();
	return res.json();
}

async function settled(request, run) {
	await expect.poll(async () => {
		const detail = await (await request.get(`/api/v1/runs/${run}`)).json();
		return detail.summary?.state !== "PENDING" && !detail.tests.some((item) => item.triage?.state === "PENDING");
	}).toBeTruthy();
}

async function cleanup(request, run, fake) {
	fake.release();
	try { if (run) await settled(request, run); }
	finally { try { await request.delete("/api/v1/settings/ai"); } finally { await fake.close(); } }
}

async function scenario(request, name, fake) {
	const { run_id: run } = await json(request, "POST", "/api/v1/runs", { name });
	const { test_id: id } = await json(request, "POST", `/api/v1/runs/${run}/tests`, { name: "evidence" });
	await json(request, "PATCH", `/api/v1/tests/${id}/finish`, { status: "FAIL", error_message: "OLD_EVIDENCE" });
	await json(request, "PATCH", `/api/v1/runs/${run}/finish`, {});
	await settled(request, run); // el trabajo sin IA termina antes de cambiar de proveedor
	await json(request, "PUT", "/api/v1/settings", { ai: { provider: "openai_compatible", model: "fake-freshness", base_url: fake.url } });
	return { run, id };
}

const runWithNewEvidence = (page, run) => page.waitForResponse(async (res) => {
	if (new URL(res.url()).pathname !== `/api/v1/runs/${run}`) return false;
	return JSON.stringify(await res.json()).includes("NEW_EVIDENCE");
});

for (const polling of [false, true]) {
	test(`el escalamiento se invalida con evidencia nueva (${polling ? "poll sin SSE" : "SSE"}), sin generar IA automáticamente`, async ({ page, request }) => {
		const fake = await provider();
		let run;
		try {
			const data = await scenario(request, `freshness ${polling}`, fake);
			run = data.run; const id = data.id;
			if (polling) await page.route("**/api/v1/stream", (route) => route.abort());
			await page.goto(`/#run=${run}&view=escalate`);
			await page.locator("[data-esc-gen]").click();
			await expect(page.locator("#esc-card")).toContainText("OLD_EVIDENCE_SUMMARY");
			expect(fake.calls()).toBe(1);
			// Una nueva lectura de la misma evidencia conserva el resumen y no gasta IA.
			await page.locator('[data-view="tests"]').click();
			await page.locator('[data-view="escalate"]').click();
			await expect(page.locator("#esc-card")).toContainText("OLD_EVIDENCE_SUMMARY");
			expect(fake.calls()).toBe(1);
			const fresh = runWithNewEvidence(page, run);
			await json(request, "PATCH", `/api/v1/tests/${id}/finish`, { status: "FAIL", error_message: "NEW_EVIDENCE" });
			await fresh;
			await expect(page.locator("#esc-card")).toHaveCount(0);
			await expect(page.locator("[data-esc-gen]")).toBeEnabled();
			expect(fake.calls()).toBe(1);
			await page.locator("[data-esc-gen]").click();
			await expect(page.locator("#esc-card")).toContainText("NEW_EVIDENCE_SUMMARY");
			expect(fake.calls()).toBe(2);
		} finally { await cleanup(request, run, fake); }
	});
}

test("una generación con evidencia antigua no restaura el escalamiento después del cambio", async ({ page, request }) => {
	const fake = await provider(true);
	let run;
	try {
		const data = await scenario(request, "late generation", fake);
		run = data.run; const id = data.id;
		await page.goto(`/#run=${run}&view=escalate`);
		const answer = page.waitForResponse((res) => new URL(res.url()).pathname === "/api/v1/ui/escalate" && res.request().method() === "POST");
		await page.locator("[data-esc-gen]").click(); await fake.first;
		const fresh = runWithNewEvidence(page, run);
		await json(request, "PATCH", `/api/v1/tests/${id}/finish`, { status: "FAIL", error_message: "NEW_EVIDENCE" });
		await fresh;
		fake.release(); await answer;
		await expect(page.locator("[data-esc-gen]")).toBeEnabled();
		await expect(page.locator("#esc-card")).toHaveCount(0);
		await page.locator("[data-esc-gen]").click();
		await expect(page.locator("#esc-card")).toContainText("NEW_EVIDENCE_SUMMARY");
		expect(fake.calls()).toBe(2);
	} finally { await cleanup(request, run, fake); }
});

test("una respuesta de cache antigua no restaura el escalamiento después del cambio", async ({ page, request }) => {
	const fake = await provider();
	let release, run;
	try {
		const data = await scenario(request, "late cache", fake);
		run = data.run; const id = data.id;
		await json(request, "POST", "/api/v1/ui/escalate", { run_id: run, test_id: 0, audience: "business", lang: "es" });
		let arrived, done, held = false;
		const waiting = new Promise((resolve) => { arrived = resolve; });
		const gate = new Promise((resolve) => { release = resolve; });
		const delivered = new Promise((resolve) => { done = resolve; });
		await page.route(`**/api/v1/runs/${run}/escalation?*`, async (route) => {
			if (held) return route.continue();
			held = true; const response = await route.fetch(); expect(response.status()).toBe(200);
			arrived(); await gate; await route.fulfill({ response }); done();
		});
		await page.goto(`/#run=${run}&view=escalate`); await waiting;
		const fresh = runWithNewEvidence(page, run);
		await json(request, "PATCH", `/api/v1/tests/${id}/finish`, { status: "FAIL", error_message: "NEW_EVIDENCE" });
		await fresh; release(); await delivered;
		await expect(page.locator("#esc-card")).toHaveCount(0);
		await page.locator("[data-esc-gen]").click();
		await expect(page.locator("#esc-card")).toContainText("NEW_EVIDENCE_SUMMARY");
		expect(fake.calls()).toBe(2);
	} finally { if (release) release(); await cleanup(request, run, fake); }
});
