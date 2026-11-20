// Espera a que el servidor termine el análisis de IA en segundo plano de una ejecución cerrada (el de
// cada test que falló y el resumen de la ejecución), aunque termine con error. Al terminar llega el
// evento «summary» o «triage», y la vista de escalamiento lo toma como evidencia nueva y descarta la
// tarjeta ya generada: un test que generaba antes de ese evento perdía la tarjeta (fallaba al azar en la CI).
const { expect } = require("@playwright/test");

async function settled(request, run) {
	await expect.poll(async () => {
		const detail = await (await request.get(`/api/v1/runs/${run}`)).json();
		return detail.summary?.state !== "PENDING" && !detail.tests.some((t) => t.triage?.state === "PENDING");
	}, { timeout: 25_000 }).toBe(true); // dentro de los 30 s del beforeAll
}

module.exports = { settled };
