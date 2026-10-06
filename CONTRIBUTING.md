# Contributing to TraceReports

Thanks for helping! Bug reports, ideas, docs, translations and code are all welcome.

> 🇪🇸 **¿Hablas español?** Puedes abrir issues y pull requests en español; el proyecto nació en
> español y toda la documentación existe en los dos idiomas.

## Ways to help

- ⭐ **Star the repo** and share it with other QA engineers. It really helps.
- 🐛 **Report a bug** with the [bug report template](https://github.com/josemiguellopez/tracereports/issues/new?template=bug_report.yml).
- 💡 **Propose a feature** with the [feature request template](https://github.com/josemiguellopez/tracereports/issues/new?template=feature_request.yml).
- 📝 **Improve the docs**, fix a typo or add an example for your framework.
- 🌐 **Translate the UI**: English and Spanish today, other languages welcome.
- 🧑‍💻 **Write code**: look for issues labeled
  [`good first issue`](https://github.com/josemiguellopez/tracereports/labels/good%20first%20issue)
  or [`help wanted`](https://github.com/josemiguellopez/tracereports/labels/help%20wanted).

For anything bigger than a small fix, **open an issue first** so we can agree on the approach before
you invest time in it.

## Project layout

```
cmd/                 server entry point
internal/api         REST API, authentication, settings, ZIP export
internal/db          SQLite: runs, tests, steps, network, history, metrics
internal/ai          AI diagnosis (Gemini, Claude, OpenAI, compatible APIs, Ollama)
internal/locator     selector suggestions when a locator breaks
internal/live        live events (Server-Sent Events)
internal/notify      Teams and Slack notifications
internal/redact      masking of secrets before anything is stored
web/                 UI: HTML/CSS/JS with no build step, embedded in the binary
test/web             unit tests of the UI logic (node:test, no dependencies)
test/e2e             UI smoke tests against a real server (Playwright)
client/python        Python client + pytest plugin
client/js            JavaScript/TypeScript client + Playwright Test reporter
client/java          Java client + JUnit 5 extension
client/go            Go client
examples/            runnable examples against the public OrangeHRM demo
docs/en, docs/es     documentation in English and Spanish
```

## Development setup

You only need the toolchain of the part you are changing.

| Part | Requirements | Run | Test |
| --- | --- | --- | --- |
| Server + UI | Go 1.26+ | `go run ./cmd` → <http://localhost:8080> | `go vet ./... && go test ./...` |
| Python client | Python 3.9+ | `pip install -e ./client/python pytest` | `pytest client/python/tests` |
| JavaScript client | Node.js 22+ | — | `cd client/js && npm test` |
| Java client | JDK 17+ | — | `cd client/java && ./gradlew test` |
| Go client | Go 1.22+ | — | `cd client/go && go test ./...` |
| UI logic | Node.js 22+ | — | `node --test "test/web/*.test.js"` |
| UI smoke (end to end) | Go 1.26+, Node.js 22+ | — | `cd test/e2e && npm ci && npx playwright install chromium && npx playwright test` |

To see your changes with real data, start the server and run one of the
[examples](examples) against it.

### Working on the UI

- The UI lives in [`web/`](web) and has **no build step**: plain HTML, CSS and JavaScript.
- It is embedded in the binary with `go:embed`, so **restart the server** (`go run ./cmd`) to see
  your changes.
- Colors, spacing and components come from design tokens. Read
  [UI tokens and components](docs/en/ui-tokens.md) before adding styles or a new theme.

### Translations

The UI is written in Spanish and translated to English in the DOM by `web/i18n.js`, using the
dictionary in [`web/i18n.en.js`](web/i18n.en.js).

- New UI text → add its English translation to `EN.exact`, or to `EN.patterns` if it carries data
  (numbers, names).
- User content (test names, steps, errors, AI text) is never translated.

Details in [UI tokens → Language](docs/en/ui-tokens.md#language-i18n).

## Pull requests

1. Fork the repo and create a branch from **`dev`** (not `main`): `git switch -c fix/short-description dev`.
2. Make your change, with tests when it changes behavior.
3. Run the tests of the parts you touched (see the table above). CI runs all of them on every PR.
4. If you change docs, update **both** `docs/en` and `docs/es` (a rough translation is fine: we
   will polish it in review).
5. Open the PR **against `dev`** and fill in the template.

`main` only receives `dev` once it is stable, so the README on the front page always matches a
working version.

### Commit messages

We use [Conventional Commits](https://www.conventionalcommits.org):

```
feat(api): filter runs by branch
fix(client-python): retry when the server answers 503
docs: add a Cypress example
```

Common types: `feat`, `fix`, `docs`, `test`, `refactor`, `ci`, `build`, `chore`. The scope is the
part you changed: `api`, `db`, `ai`, `web`, `server`, `client-python`, `client-js`, `client-java`,
`client-go`, `examples`.

### Privacy first

TraceReports stores evidence from real test runs, so:

- Never log or store secrets in clear text. Use the masking in `internal/redact` (server) and in
  the clients, and add a test when you capture a new kind of data.
- Don't add telemetry or calls to external services that the user did not turn on.

## Security issues

Please **don't open a public issue** for vulnerabilities. Follow the [security policy](SECURITY.md).

## Code of conduct

This project follows the [Contributor Covenant](CODE_OF_CONDUCT.md). Be kind: everyone here is
trying to make failing tests a little less painful.

## License

By contributing, you agree that your contributions are licensed under the
[Apache-2.0 license](LICENSE).
