#!/usr/bin/env node
// La suite e2e en tandas paralelas: compila el servidor una vez y corre N procesos de Playwright a la
// vez, cada uno con su propio servidor, su base temporal y sus puertos. Nada se comparte entre tandas.
// Los archivos se reparten por lo que tardó cada uno la última vez (e2e-timings.json), para que las
// tandas terminen juntas; dentro de cada tanda van en orden alfabético (los zz_ siguen al final).
//
//   node run.js                 # E2E_SHARDS tandas en serie (por defecto 3) y una en paralelo
//
// Un archivo con test.describe.configure({ mode: "parallel" }) (todo simulado, no escribe en el servidor)
// va a una tanda aparte con E2E_PAR_WORKERS navegadores (por defecto 3), que corren sus tests a la vez.
//   node run.js lab.spec.js     # solo esos archivos (también en tandas)
//
// Sin run.js (npx playwright test) todo sigue como siempre: una tanda, go run.
const { spawn, execFileSync } = require("node:child_process");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");

const shards = Math.max(1, Number(process.env.E2E_SHARDS || 3));
const parWorkers = Math.max(1, Number(process.env.E2E_PAR_WORKERS || 3));
const picked = process.argv.slice(2).filter((a) => a.endsWith(".spec.js"));
const extra = process.argv.slice(2).filter((a) => !a.endsWith(".spec.js"));
const root = path.join(__dirname, "..", "..");
const timingsFile = path.join(__dirname, "e2e-timings.json");
const t0 = Date.now();

// los archivos y lo que tardó cada uno la última vez (uno nuevo: el promedio)
const files = picked.length ? picked : fs.readdirSync(__dirname).filter((f) => f.endsWith(".spec.js")).sort();
let timings = {};
try { timings = JSON.parse(fs.readFileSync(timingsFile, "utf8")); } catch { /* primera vez */ }
const known = Object.values(timings);
const avg = known.length ? known.reduce((a, b) => a + b, 0) / known.length : 10_000;
const parallel = files.filter((f) => /mode:\s*"parallel"/.test(fs.readFileSync(path.join(__dirname, f), "utf8")));
const serial = files.filter((f) => !parallel.includes(f));
// el más largo primero, a la tanda que lleva menos (LPT)
const groups = Array.from({ length: Math.min(shards, serial.length) }, () => ({ files: [], ms: 0, workers: 1 }));
for (const f of [...serial].sort((a, b) => (timings[b] ?? avg) - (timings[a] ?? avg))) {
	const g = groups.reduce((a, b) => (b.ms < a.ms ? b : a));
	g.files.push(f);
	g.ms += timings[f] ?? avg;
}

if (parallel.length) groups.push({ files: parallel, ms: 0, workers: parWorkers });

// el servidor, una vez para todas las tandas (antes, cada una con go run)
const binDir = fs.mkdtempSync(path.join(os.tmpdir(), "tracereports-e2e-bin-"));
const bin = path.join(binDir, process.platform === "win32" ? "tracereports.exe" : "tracereports");
execFileSync("go", ["build", "-o", bin, "./cmd"], { cwd: root, stdio: "inherit" });
const built = Date.now();

const npx = process.platform === "win32" ? "npx.cmd" : "npx";
const runs = groups.map((g, i) => new Promise((resolve) => {
	const base = 8300 + i * 10; // servidor, GitHub falso y backend del Lab de cada tanda
	const env = { ...process.env, E2E_SERVER_BIN: bin, E2E_PORT: String(base), E2E_FAKE_PORT: String(base + 1), E2E_LAB_PORT: String(base + 2),
		PLAYWRIGHT_JSON_OUTPUT_NAME: path.join(binDir, `shard-${i}.json`), E2E_WORKERS: String(g.workers) };
	for (const k of ["E2E_DATA_DIR", "E2E_BASE_URL", "E2E_FAKE_GITHUB", "E2E_FAKE_LAB"]) delete env[k]; // cada tanda las suyas
	const list = [...g.files].sort(); // en orden: los zz_ al final, como siempre
	const p = spawn(npx, ["playwright", "test", "--reporter=line,json", ...extra, ...list], { cwd: __dirname, env, shell: process.platform === "win32" });
	let out = "";
	p.stdout.on("data", (d) => { out += d; });
	p.stderr.on("data", (d) => { out += d; });
	const start = Date.now();
	p.on("close", (code) => resolve({ i, code, out, ms: Date.now() - start, files: list }));
}));

/** Lo que tardó cada archivo, del reporte JSON de una tanda. */
function fileTimes(json) {
	const out = {};
	const walk = (suite, file) => {
		const f = suite.file ? path.basename(suite.file) : file;
		for (const spec of suite.specs || []) for (const t of spec.tests || []) for (const r of t.results || []) out[f] = (out[f] || 0) + (r.duration || 0);
		for (const s of suite.suites || []) walk(s, f);
	};
	for (const s of json.suites || []) walk(s, "");
	return out;
}

Promise.all(runs).then((results) => {
	let failed = 0, passed = 0;
	const fresh = { ...timings };
	for (const r of results) {
		const sum = r.out.match(/(\d+) passed/), bad = r.out.match(/(\d+) failed/);
		passed += sum ? Number(sum[1]) : 0;
		failed += bad ? Number(bad[1]) : 0;
		console.log(`tanda ${r.i + 1}/${groups.length}${groups[r.i].workers > 1 ? ` (${groups[r.i].workers} en paralelo)` : ""}: ${(r.ms / 1000).toFixed(0)} s, ${sum ? sum[1] : 0} passed${bad ? `, ${bad[1]} failed` : ""} · ${r.files.join(" ")}`);
		try { Object.assign(fresh, fileTimes(JSON.parse(fs.readFileSync(path.join(binDir, `shard-${r.i}.json`), "utf8")))); } catch { /* sin reporte */ }
		if (r.code !== 0) {
			console.log(`\n──── tanda ${r.i + 1}: código ${r.code} ────`);
			console.log(r.out.split("\n").filter((l) => !/^\[WebServer\].*(INFO|WARN)/.test(l)).join("\n"));
		}
	}
	// los tiempos para repartir mejor la próxima vez (un archivo que ya no existe se olvida)
	const keep = Object.fromEntries(Object.entries(fresh).filter(([f]) => fs.existsSync(path.join(__dirname, f))).sort().map(([f, ms]) => [f, Math.round(ms)]));
	fs.writeFileSync(timingsFile, JSON.stringify(keep, null, "\t") + "\n");
	const s = (ms) => (ms / 1000).toFixed(1);
	console.log(`\n${passed} passed, ${failed} failed en ${groups.length} tanda(s) · compilar ${s(built - t0)} s · tests ${s(Date.now() - built)} s · total ${s(Date.now() - t0)} s`);
	try { fs.rmSync(binDir, { recursive: true, force: true }); } catch { /* en uso */ }
	process.exit(results.some((r) => r.code !== 0) ? 1 : 0);
});
