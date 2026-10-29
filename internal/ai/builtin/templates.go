package builtin

// text is the conclusion of a rule ({name} are its vars). Fragments (an outcome, a kind of
// network error) only use Summary.
type text struct{ Summary, Suggestion string }

// texts holds every rule and fragment in both languages: a test checks that the two catalogs
// have the same keys and the same {vars}.
var texts = map[string]map[string]text{
	"es": {
		"backend_5xx": {
			"El backend respondió con un error justo antes del fallo: {call}. El test falló como consecuencia; no parece un problema del test ni del selector.",
			"Revisa los logs del servicio de {endpoint} en ese momento (usa el trace o request id de la llamada) y reprodúcela desde el Lab."},
		"backend_rate_limited": {
			"El backend limitó las peticiones justo antes del fallo: {call}.",
			"Baja el paralelismo de la suite o pide más cuota para el ambiente de pruebas; si el límite es esperado, reintenta con espera."},
		"network_unreachable": {
			"No se pudo llegar a {target}: {kind}. Es un problema del ambiente o de la red, no de la aplicación ni del test. Evidencia: {call}.",
			"Verifica que {target} esté desplegado y accesible desde donde corren los tests (DNS, firewall, proxy, certificado) antes de volver a correr."},
		"backend_no_response": {
			"El backend no respondió a tiempo justo antes del fallo: {call}.",
			"Revisa si el servicio de {endpoint} estaba caído o saturado (salud, CPU, conexiones a la base) y compara con su tiempo de respuesta habitual."},
		"auth_rejected": {
			"El backend rechazó la sesión o las credenciales del test: {call}.",
			"Revisa el usuario y la clave de prueba, que el token no haya expirado y que el usuario tenga permisos en este ambiente."},
		"backend_status": {
			"El test esperaba otra respuesta y recibió HTTP {actual}: el servidor falló al procesar la petición ({error}).",
			"Busca en los logs del backend el error de esa petición: el test solo lo detectó."},
		"network_error": {
			"No se pudo llegar a {target}: {kind}. Es un problema del ambiente o de la red, no de la aplicación ni del test. Evidencia: {call}.",
			"Verifica que {target} esté desplegado y accesible desde donde corren los tests (DNS, firewall, proxy, certificado) antes de volver a correr."},
		"js_error": {
			"La página lanzó un error de JavaScript no capturado durante el test ({console}); probablemente por eso el elemento esperado no apareció.",
			"Abre la consola del test y corrige el error de la aplicación: cambiar el selector no lo arreglaría."},
		"backend_client_error": {
			"El backend rechazó una petición antes del fallo y la aplicación no se recuperó: {call}.",
			"Revisa los datos que envió el test (cuerpo y parámetros) y el contrato del endpoint {endpoint}."},
		"locator_not_found": {
			"El test no encontró {selector}: el selector ya no coincide con la página (cambió la interfaz) o el elemento no llegó a mostrarse.",
			"Compara el selector con la captura del fallo y actualízalo; si TraceReports propone uno nuevo, revísalo en «Locator recomendado»."},
		"assertion": {
			"Una verificación del test falló: {detail}. La aplicación devolvió un valor distinto al esperado (dato o lógica).",
			"Confirma si el valor esperado sigue siendo correcto; si lo es, es un bug de la aplicación{location}."},
		"environment_down": {
			"Falló el {pct} % de los tests de la ejecución ({failed} de {total}): el ambiente probablemente estaba caído o inestable, más que este test en particular.",
			"Revisa la salud del ambiente (despliegue, base de datos, servicios de los que depende) y vuelve a correr la ejecución completa."},
		"timeout": {
			"El test agotó su tiempo de espera sin evidencia de red que explique la demora: {error}.",
			"Revisa si el backend o la página estaban lentos (tiempos de las llamadas en la pestaña Red) antes de subir el timeout."},
		"flaky": {
			"El test es inestable: su historial alterna entre pasar y fallar ({flips} cambios recientes) y este fallo no trae evidencia concluyente.",
			"Busca esperas fijas, datos compartidos entre tests o dependencias de orden; mientras tanto, considera ponerlo en cuarentena."},
		DefaultRule: {
			"Ninguna regla del motor local explica el fallo con la evidencia disponible. Mejor evidencia: {error}.",
			"Revisa el último paso y la captura del fallo; para una explicación redactada, configura un proveedor de IA (también local, como Ollama o un servidor compatible con OpenAI)."},

		"flaky_note":      {"Además, el test es inestable ({flips} cambios recientes entre pasar y fallar).", ""},
		"no_response":     {"sin respuesta", ""},
		"before_failure":  {", {secs} s antes del fallo", ""},
		"kind_dns":        {"el nombre no resuelve (DNS)", ""},
		"kind_refused":    {"la conexión fue rechazada (servicio caído o puerto equivocado)", ""},
		"kind_tls":        {"falló el certificado o el handshake TLS", ""},
		"kind_reset":      {"la conexión se cortó", ""},
		"kind_connection": {"falló la conexión", ""},
		"the_service":     {"el servicio", ""},
		"the_element":     {"el elemento esperado", ""},
		"expected_actual": {"se esperaba {expected} y se obtuvo {actual}", ""},
		"at_location":     {" (verificación en {location})", ""},
		"no_message":      {"el test no informó un mensaje de error", ""},

		"run_summary":            {"{incidents} según el motor local de reglas: {breakdown}. Revisa primero: {first}", ""},
		"run_env_down":           {"El ambiente probablemente estaba caído: falló el {pct} % de los tests ({failed} de {total}).", ""},
		"incident_1":             {"1 incidente", ""},
		"incident_n":             {"{n} incidentes", ""},
		"cat_" + CategoryLocator: {"{n} de locator", ""},
		"cat_" + CategoryBackend: {"{n} de backend", ""},
		"cat_" + CategoryLogic:   {"{n} de lógica o datos", ""},
		"cat_" + CategoryInfra:   {"{n} de ambiente o red", ""},
		"cat_none":               {"{n} sin regla concluyente", ""},
	},
	"en": {
		"backend_5xx": {
			"The backend answered with an error right before the failure: {call}. The test failed as a consequence; it does not look like a test or selector problem.",
			"Check the logs of the service behind {endpoint} at that time (use the call's trace or request id) and replay it from the Lab."},
		"backend_rate_limited": {
			"The backend rate-limited the requests right before the failure: {call}.",
			"Lower the suite's parallelism or ask for more quota in the test environment; if the limit is expected, retry with a wait."},
		"network_unreachable": {
			"{target} could not be reached: {kind}. It is an environment or network problem, not the application or the test. Evidence: {call}.",
			"Check that {target} is deployed and reachable from where the tests run (DNS, firewall, proxy, certificate) before running again."},
		"backend_no_response": {
			"The backend did not answer in time right before the failure: {call}.",
			"Check whether the service behind {endpoint} was down or overloaded (health, CPU, database connections) and compare with its usual response time."},
		"auth_rejected": {
			"The backend rejected the test's session or credentials: {call}.",
			"Check the test user and password, that the token has not expired and that the user has permissions in this environment."},
		"backend_status": {
			"The test expected another response and got HTTP {actual}: the server failed to process the request ({error}).",
			"Look for that request's error in the backend logs: the test only detected it."},
		"network_error": {
			"{target} could not be reached: {kind}. It is an environment or network problem, not the application or the test. Evidence: {call}.",
			"Check that {target} is deployed and reachable from where the tests run (DNS, firewall, proxy, certificate) before running again."},
		"js_error": {
			"The page threw an uncaught JavaScript error during the test ({console}); that is probably why the expected element never showed up.",
			"Open the test's console and fix the application error: changing the selector would not fix it."},
		"backend_client_error": {
			"The backend rejected a request before the failure and the application did not recover: {call}.",
			"Check the data the test sent (body and parameters) and the contract of the endpoint {endpoint}."},
		"locator_not_found": {
			"The test did not find {selector}: the selector no longer matches the page (the UI changed) or the element never showed up.",
			"Compare the selector with the screenshot of the failure and update it; if TraceReports suggests a new one, review it in «Recommended locator»."},
		"assertion": {
			"A check of the test failed: {detail}. The application returned a value other than the expected one (data or logic).",
			"Confirm that the expected value is still right; if it is, it is an application bug{location}."},
		"environment_down": {
			"{pct} % of the run's tests failed ({failed} of {total}): the environment was probably down or unstable, rather than this test in particular.",
			"Check the health of the environment (deployment, database, services it depends on) and run the whole suite again."},
		"timeout": {
			"The test ran out of time with no network evidence that explains the delay: {error}.",
			"Check whether the backend or the page were slow (call timings in the Network tab) before raising the timeout."},
		"flaky": {
			"The test is flaky: its history alternates between passing and failing ({flips} recent flips) and this failure has no conclusive evidence.",
			"Look for fixed waits, data shared between tests or order dependencies; meanwhile, consider quarantining it."},
		DefaultRule: {
			"No rule of the local engine explains the failure with the available evidence. Best evidence: {error}.",
			"Review the last step and the screenshot of the failure; for a written explanation, configure an AI provider (a local one too, like Ollama or an OpenAI-compatible server)."},

		"flaky_note":      {"Also, the test is flaky ({flips} recent flips between passing and failing).", ""},
		"no_response":     {"no response", ""},
		"before_failure":  {", {secs} s before the failure", ""},
		"kind_dns":        {"the name does not resolve (DNS)", ""},
		"kind_refused":    {"the connection was refused (service down or wrong port)", ""},
		"kind_tls":        {"the certificate or the TLS handshake failed", ""},
		"kind_reset":      {"the connection was cut", ""},
		"kind_connection": {"the connection failed", ""},
		"the_service":     {"the service", ""},
		"the_element":     {"the expected element", ""},
		"expected_actual": {"expected {expected} and got {actual}", ""},
		"at_location":     {" (check at {location})", ""},
		"no_message":      {"the test reported no error message", ""},

		"run_summary":            {"{incidents} according to the local rules engine: {breakdown}. Check first: {first}", ""},
		"run_env_down":           {"The environment was probably down: {pct} % of the tests failed ({failed} of {total}).", ""},
		"incident_1":             {"1 incident", ""},
		"incident_n":             {"{n} incidents", ""},
		"cat_" + CategoryLocator: {"{n} locator", ""},
		"cat_" + CategoryBackend: {"{n} backend", ""},
		"cat_" + CategoryLogic:   {"{n} logic or data", ""},
		"cat_" + CategoryInfra:   {"{n} environment or network", ""},
		"cat_none":               {"{n} with no conclusive rule", ""},
	},
}
