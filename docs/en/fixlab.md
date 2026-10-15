# FixLab: reproduce a failure and verify the fix

🌐 **English** · [Español](../es/fixlab.md)

The report tells you *why* a test failed. FixLab is the next step: **does it still fail?** From a
failed network call or a backend incident you resend the real request with a fresh session, compare
it with the run, save what a good answer is and, once someone fixes the backend, verify it. From the
report, from your terminal, from CI or from an AI agent.

> **Try it in two minutes:** `tracereports demo`, then follow the [suggested tour](demo.md#suggested-tour).
> The demo shop has a backend you can break and fix.

- [The Lab](#the-lab)
- [Checks and Verify](#checks-and-verify)
- [Export to Postman, Bruno or .http](#export-to-postman-bruno-or-http)
- [In your terminal and in CI: the capsule](#in-your-terminal-and-in-ci-the-capsule)
- [For AI agents: MCP](#for-ai-agents-mcp)
- [Security](#security)
- [Limits](#limits)
- [API](#api)

## The Lab

**Open in Lab** appears on a failed call in the **Network** tab and on a backend incident (it opens
the call that caused it).

1. **The request, as recorded.** Method, URL, headers and body. The evidence was masked when it was
   stored, so every masked value becomes a variable:
   - **Automatic**: the session (`cookie`, `authorization`) and anti-CSRF tokens (`_token`,
     `csrf…`, `xsrf…`). The Lab repeats the **recorded login** to get a fresh session (its cookies,
     or the token of its JSON answer as `Bearer`) and reads anti-CSRF tokens live from the page the
     flow starts from.
   - **Yours**: what nobody can guess, such as the password or an API key. You type it.
2. **Environment.** By default the run's host; change it to try your fix, for example
   `http://localhost:3000`.
3. **Edit** the request if you want, like in Postman: method, path, headers, body.
4. **Send.** You see each step (login, CSRF, the call), the original answer next to the new one, and
   the verdict:

| Verdict | When |
| --- | --- |
| **Reproduced** | Same status as in the run, or no answer then and now |
| **No longer fails** | It failed in the run and now answers below 400. If nobody fixed it, the failure was transient or environmental |
| **Session missing** | Now 401/403 or a redirect to the login. Check the password or type the header by hand |
| **Different error** | Another error than in the run |
| **No connection** | The backend could not be reached from where the Lab runs |

When the original answer lacks the `date` and `server` headers that every real server sends, the
Lab warns that the test probably **made it up** (a mock or `route.fulfill`).

**Copy cURL** gives the final request with `$VARIABLES`, never with the values you typed.

## Checks and Verify

After a send, **Save as a check** keeps the call (as you edited it) and what a good answer is: the
**expected status** and texts the body must **contain**.

The incident then shows **Verify (n)**: it runs its checks (asking again for the secrets, which are
never stored) and records the result on the incident, **Verified: fixed** or **Verified: still
failing**, with the before and after of each check. Checks are deleted with their run.

## Export to Postman, Bruno or .http

**Postman**, **Bruno** or **.http** download the request (as edited) with its `{{variables}}` and no
values. When there is a recorded login it comes first:

- **Postman**: a script saves its token in the `token` variable.
- **.http** (VS Code REST Client): the call reads it from the named `login` request.
- **Bruno**: it is described in the request docs.

Cookies are left to the tool.

## In your terminal and in CI: the capsule

**Download capsule** gives a JSON file with the run's context, the command that runs the test
locally and the plan of each call with its checks. It only contains masked data, like the report.
**Copy command** copies the command that reads it from the server.

```bash
# Shows how to run the failed test locally and resends each call from your machine
tracereports repro 'http://localhost:8080/api/v1/network/42/capsule'

# Runs an incident's checks; exit code 1 if one does not pass
tracereports verify 'http://localhost:8080/api/v1/runs/7/lab/capsule?incident=<key>'

# Against your local fix, with the password from the environment
TRACEREPORTS_LAB_PASSWORD=… tracereports verify -base http://localhost:3000 capsule.json
```

- The calls go **from your machine**, so they reach whatever it reaches: a VPN, `localhost`, a
  staging network.
- **Values**: `-var password=…` (repeatable), `TRACEREPORTS_LAB_<NAME>` (`TRACEREPORTS_LAB_PASSWORD`)
  or, in a terminal, typed without being shown. Flags go before or after the capsule.
- **Reading from a protected server**: `TRACEREPORTS_TOKEN` or `TRACEREPORTS_UI_USER` /
  `TRACEREPORTS_UI_PASSWORD`. They are only sent to the server of `TRACEREPORTS_URL`: a capsule URL
  from anywhere else never gets them.
- Before using a secret, the command prints where it will go (`password → POST https://…/login`).
  Check it when the capsule is not yours.

As a CI step after a deploy:

```yaml
- name: The checkout incident is fixed
  run: tracereports verify "$CAPSULE_URL"
  env:
    TRACEREPORTS_URL: ${{ vars.TRACEREPORTS_URL }}
    TRACEREPORTS_TOKEN: ${{ secrets.TRACEREPORTS_TOKEN }}
    TRACEREPORTS_LAB_PASSWORD: ${{ secrets.SHOP_QA_PASSWORD }}
    CAPSULE_URL: ${{ vars.TRACEREPORTS_URL }}/api/v1/runs/7/lab/capsule?incident=<key>
```

## For AI agents: MCP

`tracereports mcp` is an MCP server (stdio) for Claude Code or any MCP client:

| Tool | What it does |
| --- | --- |
| `list_runs` | The latest runs with their counters |
| `get_incidents` | The incidents of a run with their evidence, probable cause, checks and verification |
| `get_test` | A test's error, steps, failed calls, console and the command to run it |
| `reproduce` | Resends a call, like the Lab |
| `verify_fix` | Runs an incident's checks, to confirm a fix |

```bash
claude mcp add tracereports \
  -e TRACEREPORTS_URL=http://localhost:8080 \
  -e TRACEREPORTS_LAB_PASSWORD=… \
  -- tracereports mcp
```

Add `TRACEREPORTS_TOKEN` or `TRACEREPORTS_UI_USER` / `TRACEREPORTS_UI_PASSWORD` if the server asks for
them. It only **reads** from TraceReports; reproducing and verifying run from your machine with the
secrets of `TRACEREPORTS_LAB_<NAME>` in the MCP server's environment, so the agent never sees them.
It never changes code nor merges anything: that stays with the agent and your review.

## Security

The Lab makes the server send HTTP requests, so it is treated as an SSRF capability.

- **Who** can send: the rule of the UI actions. That means the admin token, the UI login or, without
  login, only the server's own machine. The ingest token never can.
- **Where** it may send:
  - From the server's own machine, any host, like your terminal.
  - On a shared server, only the hosts the test called plus `TRACEREPORTS_LAB_HOSTS`.
  - Link-local addresses (cloud metadata, `169.254.169.254`) and schemes other than `http`/`https`
    are always blocked.
  - Redirects are shown, not followed.
- **Secrets**: the values you type are used for that send only, never stored nor logged. The answer
  goes through the masking policy before it is shown, so a new token stays hidden.
- The **exported ZIP** has no Lab: there is no server to send from.

| Variable | Default | What it does |
| --- | --- | --- |
| `TRACEREPORTS_LAB` | on | `off` disables the Lab |
| `TRACEREPORTS_LAB_HOSTS` | — | Extra hosts a shared server's Lab may call, comma separated (`staging.acme.com`, `api.acme.com:8443`, `*.acme.internal`) |
| `TRACEREPORTS_LAB_<NAME>` | — | Values of the variables for `repro`, `verify` and `mcp` (`TRACEREPORTS_LAB_PASSWORD`) |

## Limits

- The variables come from the masking: with `TRACEREPORTS_REDACT=off` nothing is masked, so the Lab
  cannot rebuild a session.
- Tested with cookie sessions with CSRF (OrangeHRM) and JWT `Bearer` logins. OAuth with refresh
  tokens, per-request signatures (HMAC) and mTLS end up in **Session missing**; type those values by
  hand.
- It resends the calls, not the whole test. To run the test, use the command the report and
  `repro` show.

## API

| Method | Path | What it does |
| --- | --- | --- |
| GET | `/api/v1/network/{id}/lab` | The plan: the request with `{{variables}}`, the variables, the session step, the original answer, and whether you may send and why not |
| POST | `/api/v1/ui/lab/send` | `{conn_id, values, base_url?, request?}` → steps, answer and verdict |
| POST | `/api/v1/ui/lab/checks` | `{conn_id, name, request?, base_url?, expect: {status, contains?}}` saves a check |
| DELETE | `/api/v1/ui/lab/checks/{id}` | Deletes a check |
| POST | `/api/v1/ui/lab/verify` | `{run_id, incident_key, check_ids?, values, base_url?}` runs the checks and records the result |
| GET | `/api/v1/runs/{id}/lab` | Checks and last verification of each incident of the run |
| GET | `/api/v1/network/{id}/capsule` | Capsule of a call |
| GET | `/api/v1/runs/{id}/lab/capsule?incident=<key>` | Capsule of an incident, with its checks |

The `/ui/` routes need `Content-Type: application/json` and the rule of the UI actions (see
[API](api.md#actions-from-the-interface-ai-and-escalation)). Design and the measurements behind it:
[RFC](../rfc/fixlab.md).
