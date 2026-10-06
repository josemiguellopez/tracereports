// Carga los scripts de web/ (pensados para el navegador) en un contexto aislado de Node, con lo
// mínimo de DOM que necesitan al cargar. Los tests solo ejercitan la lógica pura (sin DOM real).
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

const WEB = path.join(__dirname, "..", "..", "web");

/** Lee un archivo de web/ como texto. */
const read = (name) => fs.readFileSync(path.join(WEB, name), "utf8");

/** Carga los scripts indicados (en orden) y devuelve el `window` resultante. */
function load(files, { lang = "es" } = {}) {
	const window = {};
	const ctx = {
		window,
		navigator: { language: lang },
		localStorage: { getItem: () => null, setItem() {} },
		document: { addEventListener() {}, createElement: () => ({}), documentElement: { classList: { remove() {} } } },
		MutationObserver: class { observe() {} disconnect() {} },
		URL, URLSearchParams, console,
	};
	vm.createContext(ctx);
	for (const f of files) vm.runInContext(read(f), ctx, { filename: f });
	return window;
}

module.exports = { load, read };
