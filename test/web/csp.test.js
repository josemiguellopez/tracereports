const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { read } = require("./load.js");

// El servidor manda una Content-Security-Policy sin 'unsafe-inline' para scripts: un onclick="..."
// en el HTML generado no se ejecutaría. Los eventos van por addEventListener / delegación.
test("la UI no usa handlers de eventos inline (la CSP los bloquea)", () => {
	const web = path.join(__dirname, "..", "..", "web");
	for (const f of fs.readdirSync(web).filter((n) => /\.(js|html)$/.test(n))) {
		const src = read(f);
		const m = src.match(/\son(click|change|input|load|error|submit|keydown|keyup|mouseover|focus|blur)\s*=\s*["']/i);
		assert.equal(m, null, `${f}: handler inline ${m && m[0]}`);
		assert.doesNotMatch(src, /href\s*=\s*["']javascript:/i, `${f}: href javascript:`);
	}
});

test("la casilla de comparar no abre la ejecución (delegación, sin stopPropagation inline)", () => {
	const src = read("app.js");
	assert.ok(src.includes('data-search-compare-label>'));
	assert.ok(src.includes('e.target.closest("[data-search-compare], [data-search-compare-label]")'));
});
