// Tests de humo de la UI: levanta el servidor real (go run ./cmd) con datos temporales y sin
// leer el .env del repo, carga una ejecución por la API (seed.js) y recorre la interfaz.
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { defineConfig } = require("@playwright/test");

const PORT = process.env.E2E_PORT || "8199";
// Los workers heredan el entorno: todos usan la misma carpeta que creó el proceso principal. Se
// borran las de corridas anteriores aquí y no al terminar, porque en Windows la base sigue
// abierta hasta que Playwright cierra el servidor (después del teardown).
if (!process.env.E2E_DATA_DIR) {
	// solo las de hace más de 2 h: con run.js corren varias tandas a la vez, cada una con la suya
	for (const d of fs.readdirSync(os.tmpdir()).filter((d) => d.startsWith("tracereports-e2e-"))) {
		const full = path.join(os.tmpdir(), d);
		try { if (Date.now() - fs.statSync(full).mtimeMs > 2 * 3600_000) fs.rmSync(full, { recursive: true, force: true }); } catch { /* en uso */ }
	}
	process.env.E2E_DATA_DIR = fs.mkdtempSync(path.join(os.tmpdir(), "tracereports-e2e-"));
}
process.env.E2E_BASE_URL = `http://localhost:${PORT}`;

// "GitHub" falso para los tickets: guarda lo que recibe y lo devuelve en GET /__requests. Solo lo
// levanta el proceso principal (los workers heredan E2E_FAKE_GITHUB y no lo repiten).
const FAKE_PORT = process.env.E2E_FAKE_PORT || "8198";
if (!process.env.E2E_FAKE_GITHUB) {
	process.env.E2E_FAKE_GITHUB = `http://127.0.0.1:${FAKE_PORT}`;
	const requests = [];
	require("node:http").createServer((req, res) => {
		let body = "";
		req.on("data", (c) => { body += c; });
		req.on("end", () => {
			res.setHeader("Content-Type", "application/json");
			if (req.url === "/__requests") return res.end(JSON.stringify(requests));
			// página de estado de IA falsa (formato Statuspage): las pruebas nunca consultan la real
			if (req.url === "/status/api/v2/summary.json") return res.end(JSON.stringify({
				status: { indicator: "minor", description: "Partially Degraded Service" },
				components: [{ name: "API", status: "degraded_performance" }],
				incidents: [{ name: "Elevated latency", status: "investigating", impact: "minor", shortlink: "https://status.example/i/1" }],
			}));
			requests.push({ method: req.method, url: req.url, auth: req.headers.authorization, body });
			res.statusCode = 201;
			res.end(JSON.stringify({ number: requests.length, html_url: `https://github.example/acme/shop/issues/${requests.length}` }));
		});
	}).listen(Number(FAKE_PORT), "127.0.0.1").unref();
}

// Backend falso para el Lab de FixLab (lab.spec.js): POST /api/login da un token y GET /api/orders
// lo exige; responde 500 mientras está "roto". /__broken/on y /__broken/off cambian el estado.
const LAB_PORT = process.env.E2E_LAB_PORT || "8197";
if (!process.env.E2E_FAKE_LAB) {
	process.env.E2E_FAKE_LAB = `http://127.0.0.1:${LAB_PORT}`;
	const { LAB } = require("./seed.js");
	let broken = true;
	require("node:http").createServer((req, res) => {
		let body = "";
		req.on("data", (c) => { body += c; });
		req.on("end", () => {
			res.setHeader("Content-Type", "application/json");
			res.setHeader("Server", "lab-e2e");
			if (req.url === "/__broken/on" || req.url === "/__broken/off") { broken = req.url.endsWith("on"); return res.end(JSON.stringify({ broken })); }
			if (req.url === "/api/login") {
				if (!body.includes(LAB.password)) { res.statusCode = 401; return res.end('{"error":"bad credentials"}'); }
				return res.end(JSON.stringify({ accessToken: LAB.token }));
			}
			if (req.url.startsWith("/api/orders")) {
				if (req.headers.authorization !== `Bearer ${LAB.token}`) { res.statusCode = 401; return res.end('{"error":"token expired"}'); }
				if (broken) { res.statusCode = 500; return res.end('{"error":"orders db: connection pool exhausted"}'); }
				return res.end('{"orders":[{"id":"A-1"}]}');
			}
			res.statusCode = 404;
			res.end("{}");
		});
	}).listen(Number(LAB_PORT), "127.0.0.1").unref();
}

module.exports = defineConfig({
	testDir: ".",
	// cada tanda de run.js la suya: Playwright vacía esta carpeta al empezar y se borraban los archivos de la otra
	outputDir: process.env.E2E_OUTPUT_DIR || "test-results",
	timeout: 30_000,
	expect: { timeout: 10_000 },
	fullyParallel: false,
	workers: Number(process.env.E2E_WORKERS || 1), // run.js da más a la tanda de los archivos en paralelo
	forbidOnly: !!process.env.CI,
	retries: Number(process.env.E2E_RETRIES || 0), // run.js: 1 (un test que pasa al segundo intento sale como «flaky»)
	reporter: process.env.CI ? [["list"], ["html", { open: "never" }]] : "list",
	globalSetup: require.resolve("./seed.js"),
	use: {
		baseURL: process.env.E2E_BASE_URL,
		browserName: "chromium",
		viewport: { width: 1440, height: 900 },
		locale: "es-CL",
		permissions: ["clipboard-read", "clipboard-write"],
		trace: "retain-on-failure",
		screenshot: "only-on-failure",
	},
	// sin animaciones (la app respeta prefers-reduced-motion): menos CPU y GPU y tests más rápidos. Los tests que
	// prueban una animación llevan { tag: "@motion" } y corren aparte, con movimiento
	projects: [
		{ name: "default", grepInvert: /@motion/, use: { reducedMotion: "reduce" } },
		{ name: "motion", grep: /@motion/, use: { reducedMotion: "no-preference" } },
	],
	webServer: {
		// run.js compila el servidor una vez (E2E_SERVER_BIN) para todas las tandas; solo, go run
		command: process.env.E2E_SERVER_BIN ? `"${process.env.E2E_SERVER_BIN}"` : "go run ./cmd",
		cwd: path.join(__dirname, "..", ".."),
		url: `${process.env.E2E_BASE_URL}/api/v1/runs`,
		timeout: 180_000,
		reuseExistingServer: false,
		stdout: "ignore",
		stderr: "pipe",
		env: {
			PORT,
			// solo este equipo: sin el aviso del firewall de Windows por cada binario nuevo
			TRACEREPORTS_BIND: "127.0.0.1",
			DATA_DIR: process.env.E2E_DATA_DIR,
			// nada del .env local (login, token, IA): el archivo indicado no existe
			TRACEREPORTS_ENV_FILE: path.join(process.env.E2E_DATA_DIR, "no.env"),
			TRACEREPORTS_TOKEN: "", TRACEREPORTS_UI_USER: "", TRACEREPORTS_UI_PASSWORD: "",
			AI_PROVIDER: "", AI_API_KEY: "", GEMINI_API_KEY: "", ANTHROPIC_API_KEY: "", OPENAI_API_KEY: "", OLLAMA_HOST: "",
			TEAMS_WEBHOOK_URL: "", SLACK_WEBHOOK_URL: "",
			// tickets: GitHub falso; Jira y Azure apagados
			TRACEREPORTS_GITHUB_REPO: "acme/shop", TRACEREPORTS_GITHUB_TOKEN: "e2e-token", TRACEREPORTS_GITHUB_API: process.env.E2E_FAKE_GITHUB,
			TRACEREPORTS_JIRA_URL: "", TRACEREPORTS_AZURE_URL: "", PUBLIC_URL: process.env.E2E_BASE_URL,
			// estado del proveedor de IA: la página falsa de arriba, nunca la real
			TRACEREPORTS_AI_STATUS_URL: `${process.env.E2E_FAKE_GITHUB}/status/api/v2/summary.json`,
			// En vivo: "sin señal" a los 4 s, para no esperar 3 minutos (zz_live.spec.js)
			TRACEREPORTS_NO_SIGNAL_MIN: "0.067",
			// correlación con los logs: plantillas de prueba
			TRACEREPORTS_LOGS_URL: "https://logs.example/search?q={request_id}&from={from}", TRACEREPORTS_TRACE_URL: "https://traces.example/{trace_id}",
		},
	},
});
