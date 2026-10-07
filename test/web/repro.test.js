// Tests del comando para reproducir un test en local (reproCommands, web/tracereports_features.js).
const test = require("node:test");
const assert = require("node:assert/strict");
const { load } = require("./load.js");

const { reproCommands } = load(["tracereports_features.js"]).TraceReportsFeatures;
const cmds = (o) => [...reproCommands(o)].map((c) => `${c.label}: ${c.cmd}`);

test("pytest: el nodeid, entre comillas para la shell", () => {
	assert.deepEqual(cmds({ framework: "pytest", key: "tests/test_login.py::TestLogin::test_admin[chromium-it's]" }),
		["pytest: pytest 'tests/test_login.py::TestLogin::test_admin[chromium-it'\\''s]'"]);
});

test("Playwright: archivo, título exacto y proyecto", () => {
	assert.deepEqual(cmds({ framework: "playwright", key: "tests/login.spec.js > Login > admin entra (v2) [chromium]" }),
		["Playwright: npx playwright test 'tests/login.spec.js' -g '^admin entra \\(v2\\)$' --project='chromium'"]);
	assert.deepEqual(cmds({ framework: "playwright", key: "a.spec.ts > sin proyecto" }),
		["Playwright: npx playwright test 'a.spec.ts' -g '^sin proyecto$'"]);
});

test("JUnit: Maven y Gradle", () => {
	assert.deepEqual(cmds({ framework: "junit5", key: "com.acme.LoginTest#adminEntra(java.lang.String)" }), [
		"Maven: mvn test -Dtest='LoginTest#adminEntra'",
		"Gradle: ./gradlew test --tests 'com.acme.LoginTest.adminEntra'",
	]);
});

test("Go: paquete y subtests anclados", () => {
	assert.deepEqual(cmds({ framework: "go", key: "shop/checkout/TestPay/visa" }),
		["Go: go test ./shop/checkout/... -run '^TestPay$/^visa$'"]);
});

test("con commit, primero el checkout de esa versión", () => {
	const [c] = reproCommands({ framework: "pytest", key: "t.py::test_x", commit: "abcdef1234567890abcd" });
	assert.equal(c.cmd, "git checkout abcdef123456 && pytest 't.py::test_x'");
});

test("sin datos suficientes no inventa un comando", () => {
	for (const o of [{}, { framework: "pytest" }, { framework: "pytest", key: "name:login" },
		{ framework: "cypress", key: "x" }, { framework: "go", key: "pkg/helper" }, { framework: "junit5", key: "sin-formato" }]) {
		assert.deepEqual(reproCommands(o).length, 0, JSON.stringify(o));
	}
});
