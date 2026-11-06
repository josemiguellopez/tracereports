// Tests del comando para reproducir un test en local (reproCommands, web/tracereports_features.js).
// Los mismos casos que internal/repro/repro_test.go: la vista del test y Escalar dicen lo mismo.
const test = require("node:test");
const assert = require("node:assert/strict");
const { load } = require("./load.js");

const F = load(["tracereports_features.js"]).TraceReportsFeatures;
const { reproCommands, dependsOnEarlier, orderedRepro, reproScript, reproCommitNote } = F;
const cmds = (o) => [...reproCommands(o)].map((c) => `${c.label} [${c.scope}]: ${c.cmd}`);

test("pytest: el nodeid entre comillas para la shell, y después el archivo completo", () => {
	assert.deepEqual(cmds({ framework: "pytest", key: "tests/test_login.py::TestLogin::test_admin[chromium-it's]" }), [
		"pytest [test]: pytest 'tests/test_login.py::TestLogin::test_admin[chromium-it'\\''s]'",
		"pytest [file]: pytest 'tests/test_login.py'",
	]);
	// sin "::" no hay archivo que deducir
	assert.deepEqual(cmds({ framework: "pytest", key: "test_login_admin" }), ["pytest [test]: pytest 'test_login_admin'"]);
});

test("Playwright: archivo, título exacto y proyecto; después el archivo completo", () => {
	assert.deepEqual(cmds({ framework: "playwright", key: "tests/login.spec.js > Login > admin entra (v2) [chromium]" }), [
		"Playwright [test]: npx playwright test 'tests/login.spec.js' -g '^admin entra \\(v2\\)$' --project='chromium'",
		"Playwright [file]: npx playwright test 'tests/login.spec.js' --project='chromium'",
	]);
	assert.deepEqual(cmds({ framework: "playwright", key: "a.spec.ts > sin proyecto" }), [
		"Playwright [test]: npx playwright test 'a.spec.ts' -g '^sin proyecto$'",
		"Playwright [file]: npx playwright test 'a.spec.ts'",
	]);
});

test("JUnit: Maven y Gradle, el método y después la clase", () => {
	assert.deepEqual(cmds({ framework: "junit5", key: "com.acme.LoginTest#adminEntra(java.lang.String)" }), [
		"Maven [test]: mvn test -Dtest='LoginTest#adminEntra'",
		"Gradle [test]: ./gradlew test --tests 'com.acme.LoginTest.adminEntra'",
		"Maven [class]: mvn test -Dtest='LoginTest'",
		"Gradle [class]: ./gradlew test --tests 'com.acme.LoginTest'",
	]);
});

test("Go: paquete y subtests anclados", () => {
	assert.deepEqual(cmds({ framework: "go", key: "shop/checkout/TestPay/visa" }),
		["Go [test]: go test ./shop/checkout/... -run '^TestPay$/^visa$'"]);
});

test("el commit nunca va dentro del comando: solo como nota", () => {
	for (const c of reproCommands({ framework: "pytest", key: "t.py::test_x", commit: "abcdef1234567890abcd" })) {
		assert.doesNotMatch(c.cmd, /git/);
	}
	assert.equal(reproCommitNote("abcdef1234567890abcd"), "La ejecución usó el commit abcdef123456; si tu copia está en otra versión: git checkout abcdef123456");
	assert.equal(reproCommitNote("abc1234", "en"), "The run used commit abc1234; if your copy is on another version: git checkout abc1234");
	for (const bad of ["", ";echo MARK;#", "$(id)", "main"]) assert.equal(reproCommitNote(bad), "", bad);
});

// Caso del cliente: test_002 depende de que test_001 haya creado la receta; corrido solo falla por
// otra razón. Ahí el archivo completo va primero (igual que repro.Ordered y repro.Script).
test("un test que depende de otros anteriores: primero el archivo completo", () => {
	assert.equal(dependsOnEarlier("Exception: Error número de receta no puede ser vacio"), true);
	assert.equal(dependsOnEarlier("Failed: Test 'test_002' omitido — paso anterior falló: 'test_001'"), true);
	assert.equal(dependsOnEarlier("AssertionError: assert 10 == 12"), false);
	assert.equal(dependsOnEarlier("TimeoutError: waiting for locator('#pay')"), false);
	const key = "Tests/Clinico/Farmacia/test_despachar_receta_otro_nodo.py::TestCrearReceta::test_002_despacho_receta_otro_nodo";
	const list = reproCommands({ framework: "pytest", key });
	assert.deepEqual([...orderedRepro(list, true).map((c) => c.scope)], ["file", "test"]);
	assert.deepEqual([...orderedRepro(list, false).map((c) => c.scope)], ["test", "file"]);
	assert.equal(reproScript(orderedRepro(list, true), "fd051753d33f9a", "es"),
		"# Todo el archivo (si el test depende de otros anteriores del mismo archivo) · pytest\n" +
		"pytest 'Tests/Clinico/Farmacia/test_despachar_receta_otro_nodo.py'\n\n" +
		`# Este test · pytest\npytest '${key}'\n\n` +
		"# La ejecución usó el commit fd051753d33f; si tu copia está en otra versión: git checkout fd051753d33f");
});

test("sin datos suficientes no inventa un comando", () => {
	for (const o of [{}, { framework: "pytest" }, { framework: "pytest", key: "name:login" },
		{ framework: "cypress", key: "x" }, { framework: "go", key: "pkg/helper" }, { framework: "junit5", key: "sin-formato" }]) {
		assert.deepEqual(reproCommands(o).length, 0, JSON.stringify(o));
	}
	assert.equal(reproScript([], "abc1234"), "");
});
