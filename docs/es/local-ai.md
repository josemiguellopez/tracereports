# LLM local

🌐 [English](../en/local-ai.md) · **Español**

Para un diagnóstico **redactado** sin que la evidencia salga de tu red, usa un modelo en tu propia
infraestructura. Sin configurar nada ya diagnostica el [motor local de reglas](configuration.md#motor-local-de-diagnóstico-sin-llm);
el LLM local recibe además su conclusión y la redacta o corrige.

Cualquier servidor compatible con la API de OpenAI (`/v1/chat/completions`) sirve con
`AI_PROVIDER=openai_compatible`: **llama.cpp server**, **llamafile**, **LM Studio**, **vLLM** y **LocalAI**.
Ollama tiene su propio proveedor (`AI_PROVIDER=ollama`).

## Con Docker: el perfil `llm`

El `docker-compose.yml` del repositorio trae un servicio opcional con llama.cpp server:

1. Descarga un modelo GGUF a la carpeta `models/` junto al compose (una vez, con internet):
   por ejemplo, un modelo instruct de ~3B en cuantización `Q4_K_M` (unos 2 GB).
2. En el `.env`:

   ```ini
   AI_PROVIDER=openai_compatible
   AI_BASE_URL=http://llm:8080/v1
   AI_MODEL=local
   LLM_MODEL_FILE=nombre-del-modelo.gguf   # el archivo dentro de models/
   ```

3. `docker compose --profile llm up -d`

El servicio no publica puertos (solo TraceReports lo alcanza), monta el modelo de solo lectura y no
descarga nada al correr. Corre con el mismo endurecimiento que TraceReports (`read_only`, sin
capabilities, `no-new-privileges`).

## Sin Docker

| Runtime | Comando de ejemplo | `AI_BASE_URL` |
|---|---|---|
| llama.cpp server | `llama-server -m modelo.gguf --port 8081 --jinja` | `http://127.0.0.1:8081/v1` |
| llamafile | `./modelo.llamafile --server --port 8081` | `http://127.0.0.1:8081/v1` |
| LM Studio | *Developer → Start server* | `http://127.0.0.1:1234/v1` |
| vLLM | `vllm serve <modelo> --port 8000` | `http://127.0.0.1:8000/v1` |
| LocalAI | `local-ai run <modelo>` | `http://127.0.0.1:8080/v1` |

Con `AI_PROVIDER=openai_compatible` el `AI_MODEL` es el nombre que espera el servidor; la API key es
opcional. Si quieres que solo se usen proveedores locales: `TRACEREPORTS_AI_ALLOWED_PROVIDERS=builtin,openai_compatible`.

## Requisitos y modelos

| Tamaño del modelo | RAM (CPU, Q4) | Tiempo por diagnóstico (CPU de 8 núcleos) | Uso |
|---|---|---|---|
| ~3B | 4 GB | 10–30 s | Redacta bien la conclusión del motor local |
| 7–8B | 8 GB | 30–90 s | Mejores hipótesis en fallos sin regla concluyente |
| 14B o más | 16 GB o GPU | con GPU, segundos | Calidad cercana a un LLM en la nube |

Son órdenes de magnitud, no garantías: dependen del hardware y del modelo. El diagnóstico corre en
segundo plano (no frena la ingesta), y `TRACEREPORTS_AI_MAX_PER_RUN` limita cuántos se piden por ejecución.

## Comparación

| | Motor local (`builtin`) | LLM local | LLM en la nube |
|---|---|---|---|
| Datos fuera del servidor | Nunca | No salen de tu red | Sí (redactados) |
| Instalación | Ninguna | Runtime + modelo | API key |
| Costo | Cero | Hardware | Por uso |
| Latencia | Milisegundos | Segundos a minutos | Segundos |
| Resultado | Determinístico, cita la regla | Redactado; varía | Redactado; el más preciso |
