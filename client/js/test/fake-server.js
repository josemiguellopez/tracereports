// Servidor de TraceReports mínimo para las pruebas: guarda lo que recibe y puede fallar a propósito.
import http from "node:http";

export async function fakeServer({ failFirst = 0, distinctIds = false, failPaths = [] } = {}) {
  const requests = [];
  let fails = failFirst;
  let nextTest = 100;
  const server = http.createServer((req, res) => {
    const chunks = [];
    req.on("data", (c) => chunks.push(c));
    req.on("end", () => {
      if (fails > 0 || failPaths.some((p) => req.url.endsWith(p))) {
        if (fails > 0) fails--;
        res.writeHead(503).end();
        return;
      }
      const body = Buffer.concat(chunks);
      requests.push({ method: req.method, path: req.url, key: req.headers["idempotency-key"], auth: req.headers.authorization, body });
      let out = {};
      if (req.url === "/api/v1/runs") out = { run_id: 7 };
      else if (req.url.endsWith("/tests")) out = { test_id: distinctIds ? nextTest++ : 11 };
      res.writeHead(201, { "Content-Type": "application/json" }).end(JSON.stringify(out));
    });
  });
  await new Promise((r) => server.listen(0, "127.0.0.1", r));
  const url = `http://127.0.0.1:${server.address().port}`;
  return {
    url,
    requests,
    json: (p) => requests.filter((r) => r.path === p).map((r) => JSON.parse(r.body.toString())),
    paths: () => requests.map((r) => `${r.method} ${r.path}`),
    failNext: (n) => { fails = n; },
    close: () => new Promise((r) => server.close(r)),
  };
}
