// Inyección en los comandos que se copian a una shell (reproCommands y curlOf). Los mismos casos que
// internal/repro/injection_test.go: el backend y la interfaz generan el mismo texto.
// Marcadores inocuos: nada se ejecuta, solo se compara el texto.
const test = require("node:test");
const assert = require("node:assert/strict");
const { load } = require("./load.js");

const { reproCommands, curlOf } = load(["tracereports_features.js"]).TraceReportsFeatures;
const cmds = (o) => [...reproCommands(o)].map((c) => c.cmd);

const hostile = [";echo MARK;#", "abc123 && echo MARK", "$(echo MARK)", "`echo MARK`", "abc'; echo MARK; '",
	"abc123\necho MARK", "abc|echo MARK", "abc123 #", "HEAD", "v1.2.3", "xyz1234", "abc"];

test("un commit que no es un id de Git nunca entra al comando", () => {
	for (const commit of hostile) {
		for (const [framework, key] of [["pytest", "test_login.py::test_admin"], ["playwright", "a.spec.js > A > b [chromium]"],
			["junit5", "com.acme.LoginTest#ok"], ["go", "shop/TestPay"]]) {
			for (const cmd of cmds({ framework, key, commit })) {
				assert.ok(!cmd.includes("MARK") && !cmd.includes("git checkout"), `${framework} ${JSON.stringify(commit)}: ${cmd}`);
			}
		}
	}
});

test("commits válidos: completo (12), abreviado, mayúsculas, SHA-256 y vacío", () => {
	assert.deepEqual(cmds({ framework: "pytest", key: "t.py::x", commit: "0123456789ABCDEF0123" }), ["git checkout 0123456789ab && pytest 't.py::x'"]);
	assert.deepEqual(cmds({ framework: "pytest", key: "t.py::x", commit: "abc1234" }), ["git checkout abc1234 && pytest 't.py::x'"]);
	assert.deepEqual(cmds({ framework: "pytest", key: "t.py::x", commit: "ab".repeat(32) }), ["git checkout abababababab && pytest 't.py::x'"]);
	assert.deepEqual(cmds({ framework: "pytest", key: "t.py::x", commit: "" }), ["pytest 't.py::x'"]);
});

test("Go: un paquete hostil va entre comillas; el normal no cambia", () => {
	assert.ok(cmds({ framework: "go", key: "shop;echo MARK;/TestPay" })[0].startsWith("go test './shop;echo MARK;/...' -run "));
	assert.ok(cmds({ framework: "go", key: "shop/$(echo MARK)/TestPay" })[0].startsWith("go test './shop/$(echo MARK)/...'"));
	assert.deepEqual(cmds({ framework: "go", key: "shop/checkout/TestPay/visa" }), ["go test ./shop/checkout/... -run '^TestPay$/^visa$'"]);
	assert.deepEqual(cmds({ framework: "go", key: "TestRoot" }), ["go test ./... -run '^TestRoot$'"]);
});

test("cURL: método y nombres de header hostiles quedan literales", () => {
	const got = curlOf({ method: "GET;echo MARK", url: "https://x/y",
		request_headers: { "X-Auth$(echo MARK)": "<masked>", 'Token"; echo MARK; "': "<masked>", "1-Token": "<masked>" } });
	for (const bad of ["-X GET;echo", '"X-Auth$(echo MARK)', '"Token"; echo']) assert.ok(!got.includes(bad), `${bad}:\n${got}`);
	for (const want of ["curl -X 'GET;echo MARK'", `-H 'X-Auth$(echo MARK): '"$X_AUTH_ECHO_MARK_"`, '-H "1-Token: $H_1_TOKEN"']) {
		assert.ok(got.includes(want), `${want}:\n${got}`);
	}
	const normal = curlOf({ method: "POST", url: "https://x/y", request_headers: { Authorization: "<masked>" } });
	assert.ok(normal.startsWith("curl -X POST 'https://x/y'") && normal.includes('-H "Authorization: $AUTHORIZATION"'), normal);
});
