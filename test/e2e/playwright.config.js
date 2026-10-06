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
	for (const d of fs.readdirSync(os.tmpdir()).filter((d) => d.startsWith("tracereports-e2e-"))) {
		try { fs.rmSync(path.join(os.tmpdir(), d), { recursive: true, force: true }); } catch { /* en uso */ }
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
			requests.push({ method: req.method, url: req.url, auth: req.headers.authorization, body });
			res.statusCode = 201;
			res.end(JSON.stringify({ number: requests.length, html_url: `https://github.example/acme/shop/issues/${requests.length}` }));
		});
	}).listen(Number(FAKE_PORT), "127.0.0.1").unref();
}

module.exports = defineConfig({
	testDir: ".",
	timeout: 30_000,
	expect: { timeout: 10_000 },
	fullyParallel: false,
	workers: 1,
	forbidOnly: !!process.env.CI,
	retries: 0,
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
	webServer: {
		command: "go run ./cmd",
		cwd: path.join(__dirname, "..", ".."),
		url: `${process.env.E2E_BASE_URL}/api/v1/runs`,
		timeout: 180_000,
		reuseExistingServer: false,
		stdout: "ignore",
		stderr: "pipe",
		env: {
			PORT,
			DATA_DIR: process.env.E2E_DATA_DIR,
			// nada del .env local (login, token, IA): el archivo indicado no existe
			TRACEREPORTS_ENV_FILE: path.join(process.env.E2E_DATA_DIR, "no.env"),
			TRACEREPORTS_TOKEN: "", TRACEREPORTS_UI_USER: "", TRACEREPORTS_UI_PASSWORD: "",
			AI_PROVIDER: "", AI_API_KEY: "", GEMINI_API_KEY: "", ANTHROPIC_API_KEY: "", OPENAI_API_KEY: "", OLLAMA_HOST: "",
			TEAMS_WEBHOOK_URL: "", SLACK_WEBHOOK_URL: "",
			// tickets: GitHub falso; Jira y Azure apagados
			TRACEREPORTS_GITHUB_REPO: "acme/shop", TRACEREPORTS_GITHUB_TOKEN: "e2e-token", TRACEREPORTS_GITHUB_API: process.env.E2E_FAKE_GITHUB,
			TRACEREPORTS_JIRA_URL: "", TRACEREPORTS_AZURE_URL: "", PUBLIC_URL: process.env.E2E_BASE_URL,
		},
	},
});
