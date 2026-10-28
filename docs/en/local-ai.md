# Local LLM

🌐 **English** · [Español](../es/local-ai.md)

For a **written** diagnosis without the evidence leaving your network, use a model on your own
infrastructure. With nothing configured the [local rules engine](configuration.md#local-diagnosis-engine-no-llm)
already diagnoses; the local LLM also gets its conclusion and writes it up or corrects it.

Any server compatible with the OpenAI API (`/v1/chat/completions`) works with
`AI_PROVIDER=openai_compatible`: **llama.cpp server**, **llamafile**, **LM Studio**, **vLLM** and **LocalAI**.
Ollama has its own provider (`AI_PROVIDER=ollama`).

## With Docker: the `llm` profile

The repository's `docker-compose.yml` has an optional llama.cpp server service:

1. Download a GGUF model to the `models/` folder next to the compose file (once, with internet):
   for example, a ~3B instruct model in `Q4_K_M` quantization (about 2 GB).
2. In the `.env`:

   ```ini
   AI_PROVIDER=openai_compatible
   AI_BASE_URL=http://llm:8080/v1
   AI_MODEL=local
   LLM_MODEL_FILE=model-name.gguf   # the file inside models/
   ```

3. `docker compose --profile llm up -d`

The service publishes no port (only TraceReports reaches it), mounts the model read-only and downloads
nothing at runtime. It runs with the same hardening as TraceReports (`read_only`, no capabilities,
`no-new-privileges`).

## Without Docker

| Runtime | Example command | `AI_BASE_URL` |
|---|---|---|
| llama.cpp server | `llama-server -m model.gguf --port 8081 --jinja` | `http://127.0.0.1:8081/v1` |
| llamafile | `./model.llamafile --server --port 8081` | `http://127.0.0.1:8081/v1` |
| LM Studio | *Developer → Start server* | `http://127.0.0.1:1234/v1` |
| vLLM | `vllm serve <model> --port 8000` | `http://127.0.0.1:8000/v1` |
| LocalAI | `local-ai run <model>` | `http://127.0.0.1:8080/v1` |

With `AI_PROVIDER=openai_compatible`, `AI_MODEL` is the name the server expects; the API key is optional.
To allow only local providers: `TRACEREPORTS_AI_ALLOWED_PROVIDERS=builtin,openai_compatible`.

## Requirements and models

| Model size | RAM (CPU, Q4) | Time per diagnosis (8-core CPU) | Use |
|---|---|---|---|
| ~3B | 4 GB | 10–30 s | Writes up the local engine's conclusion well |
| 7–8B | 8 GB | 30–90 s | Better hypotheses on failures with no conclusive rule |
| 14B or more | 16 GB or a GPU | seconds with a GPU | Quality close to a cloud LLM |

These are orders of magnitude, not guarantees: they depend on the hardware and the model. The diagnosis
runs in the background (it does not slow ingestion), and `TRACEREPORTS_AI_MAX_PER_RUN` limits how many are
requested per run.

## Comparison

| | Local engine (`builtin`) | Local LLM | Cloud LLM |
|---|---|---|---|
| Data leaving the server | Never | Stays in your network | Yes (redacted) |
| Setup | None | Runtime + model | API key |
| Cost | Zero | Hardware | Per use |
| Latency | Milliseconds | Seconds to minutes | Seconds |
| Result | Deterministic, cites the rule | Written; varies | Written; the most accurate |
