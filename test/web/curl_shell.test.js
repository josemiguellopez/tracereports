// curlOf: método y nombres de header que son tokens HTTP válidos pero tienen significado para la
// shell. Los mismos casos que internal/repro/shell_token_test.go. Se comparan como texto.
const test = require("node:test");
const assert = require("node:assert/strict");
const { load } = require("./load.js");

const { curlOf } = load(["tracereports_features.js"]).TraceReportsFeatures;
const shq = (s) => `'${String(s).replace(/'/g, `'\\''`)}'`;
const envVar = (k) => { const v = k.toUpperCase().replace(/\W+/g, "_"); return !v || /^\d/.test(v) ? `H_${v}` : v; };

test("el método es siempre un argumento literal", () => {
	for (const m of ["GET|id", "GET&echo", "GET`id`", "GET$HOME", "GET!x", "GET'x", "GET*", "~GET", "GET#x", "GET^x"]) {
		const got = curlOf({ method: m, url: "https://example.test/" });
		assert.ok(got.startsWith(`curl -X ${shq(m)} 'https://example.test/'`), got);
	}
	for (const m of ["GET", "POST", "PATCH", "DELETE", "PROPFIND", "M-SEARCH"]) {
		assert.ok(curlOf({ method: m, url: "https://x/" }).startsWith(`curl -X ${m} 'https://x/'`), m);
	}
});

test("el nombre del header es literal y solo se expande la variable generada", () => {
	for (const n of ["X-Auth-`id`", "X-Auth-$UID", "X-Auth!x", "X-A|b", "X-A&b", "X-'q", "X-A^b", "X-A~b", "X-A#b", "X-A*b"]) {
		const got = curlOf({ method: "GET", url: "https://x/", request_headers: { [n]: "<masked>" } });
		const want = `-H ${shq(`${n}: `)}"$${envVar(n)}"`;
		assert.ok(got.includes(want), `${n}:\n${got}\nwant ${want}`);
		assert.ok(!got.includes(`-H "${n}`), got);
		assert.match(envVar(n), /^[A-Z_][A-Z0-9_]*$/);
	}
	const normal = curlOf({ method: "GET", url: "https://x/", request_headers: { Authorization: "<masked>", "X-Api-Key": "k" } });
	assert.ok(normal.includes('-H "Authorization: $AUTHORIZATION"') && normal.includes('-H "X-Api-Key: $X_API_KEY"'), normal);
});
