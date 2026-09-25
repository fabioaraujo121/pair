# Item 7 — Prompt behaviour: narrate calls per edit, narration length, and the 400-char cap

Generated 2026-09-25T02:12:43.809Z. Scratch dir: `/private/tmp/claude-501/-Users-fabioaraujo-Developer/c2b4051e-b6e0-4ea5-b4af-94b87a3a9700/scratchpad/validation/item7`.

## Setup

- Claude Code 2.1.282, macOS 26.6.2 arm64, Node v24.16.0. Fake MCP server in Node (`server.mjs`, @modelcontextprotocol/sdk 1.30.1 + zod 4.6.5) exposing `narrate`, `show`, `ask`; it only logs to `calls.jsonl` and never enforces the 400-char cap.
- Sample project `proj/` (plain Node HTTP users API, 6 files, node:test), with the PRD's CLAUDE.md pairing block copied verbatim between `<!-- pair:start -->`/`<!-- pair:end -->`, an empty-section `PAIRING.md`, and a project-level `PreToolUse` hook on `Edit|Write|MultiEdit` that appends `{ts, run, tool, file}` to the same `calls.jsonl` so edits and narrations share one ordered log. Each run used its own copy (`projA`, `projB`, `projC`).
- MCP server registered only through `--mcp-config mcp.json --strict-mcp-config`; no user config was touched. macOS has no `timeout`, so `bin/timeout` is a 3-line perl `alarm` shim (verified: `timeout 2 sleep 5` exits 142).
- Nested sessions launched with `env -u CLAUDECODE ...`; that alone was sufficient (no need to unset `CLAUDE_CODE_ENTRYPOINT`). Stderr only carried the harmless 'no stdin data received in 3s' warning (add `< /dev/null` next time).
- Standalone MCP check before involving claude: hand-crafted `initialize`, `notifications/initialized`, `tools/list`, `tools/call` over stdin returned the three tools and logged the calls (`selftest.jsonl`). Smoke run with `--model haiku` confirmed the nested claude sees `pair` as `connected` and lists `mcp__pair__ask/narrate/show` in the init message.
- Observation: in Claude Code 2.1.282 MCP tool schemas are deferred; every run spent its first tool call on `ToolSearch select:mcp__pair__ask,mcp__pair__narrate,mcp__pair__show` before it could use them.

## Commands

```sh
#!/bin/sh
# usage: run.sh <ID> <task> [extra claude args...]
S=/private/tmp/claude-501/-Users-fabioaraujo-Developer/c2b4051e-b6e0-4ea5-b4af-94b87a3a9700/scratchpad/validation/item7
ID=$1; TASK=$2; shift 2
export PATH="$S/bin:$PATH"
cd "$S/proj$ID" || exit 99
START=$(date +%s)
PAIR_RUN=$ID env -u CLAUDECODE timeout 600 claude -p "$TASK" \
  --mcp-config "$S/mcp.json" --strict-mcp-config \
  --allowedTools "Edit,Write,MultiEdit,Read,Glob,Grep,Bash,mcp__pair__narrate,mcp__pair__show,mcp__pair__ask" \
  --permission-mode acceptEdits --output-format stream-json --verbose "$@" \
  > "$S/run$ID.jsonl" 2> "$S/run$ID.err"
EXIT=$?
END=$(date +%s)
printf '{"run":"%s","start":%s,"end":%s,"wall_s":%s,"exit":%s,"task":%s}\n' "$ID" "$START" "$END" "$((END-START))" "$EXIT" "$(printf '%s' "$TASK" | jq -Rs .)" > "$S/run$ID.meta"
```

Launched as:
```sh
./run.sh A "Add input validation to the user creation path: name required 2-50 chars, email must look like an email; return 422 with a JSON error list; add tests."
./run.sh B "Rename the helper \`isValidEmail\` to \`emailLooksValid\` everywhere and fix the test." --model sonnet
./run.sh C "Add rate limiting to the create endpoint."
node analyze.mjs > analysis.json && node gen_report.mjs > REPORT.md
```

## Per-run summary

| Run | Task | Model | Result | Wall s | Turns | Cost USD |
| --- | --- | --- | --- | --- | --- | --- |
| A | Add input validation to the user creation path: name required 2-50 chars, email must look like an email; return 422 with a JSON error list; add tests. | claude-fable-5-1 | success (exit 0) | 223 | 36 | 1.685 |
| B | Rename the helper `isValidEmail` to `emailLooksValid` everywhere and fix the test. | claude-sonnet-5 | success (exit 0) | 38 | 18 | 0.186 |
| C | Add rate limiting to the create endpoint. | claude-fable-5-1 | success (exit 0) | 186 | 32 | 1.460 |

## Counts: edits vs narrate / show / ask

| Run | Edits (all) | Code edits | PAIRING.md edits | narrate | show | ask | narrate/edit (all) | narrate/code edit | Edits w/o narrate immediately before | Edits w/o any narrate since previous edit | narrate w/o code edit following |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| A | 10 | 7 | 3 | 6 | 4 | 3 | 0.6 | 0.86 | 4 | 4 | 2 |
| B | 7 | 5 | 2 | 2 | 1 | 0 | 0.29 | 0.4 | 5 | 5 | 1 |
| C | 12 | 9 | 3 | 5 | 1 | 1 | 0.42 | 0.56 | 8 | 7 | 1 |
| ALL | 29 | 21 |  | 13 | 6 | 4 | 0.45 | 0.62 | 17 | 16 | 4 |

'Immediately before' = the previous logged event (narrate, show-with-note, or edit) is a narration. 'Since previous edit' = any narration between the previous edit and this one, so a single narration covering a batch of N edits counts as N-1 un-narrated edits.

## Narration length (chars) and shape

| Run | n | min | avg | median | max | >200 | >250 | >400 | sentences avg | sentences max | with markdown/backticks | with code-ish tokens | pt stopword hits | en stopword hits |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| A | 6 | 110 | 204.3 | 197 | 304 | 3 | 2 | 0 | 2.33 | 4 | 0 | 0 | 33 | 5 |
| B | 2 | 73 | 99.5 | 99.5 | 126 | 0 | 0 | 0 | 1 | 1 | 0 | 0 | 5 | 0 |
| C | 5 | 157 | 231.6 | 218 | 317 | 3 | 2 | 0 | 2.2 | 3 | 0 | 0 | 22 | 1 |
| ALL | 13 | 73 | 198.7 | 187 | 317 | 6 | 4 | 0 |  |  |  |  |  |  |

Overall p90 length: 304 chars.

## Protocol adherence

| Run | PAIRING.md written before first code edit | ask count | Decisions appended after each ask | Final Decisions section |
| --- | --- | --- | --- | --- |
| A | true | 3 | [true,true,true] | Resposta do engineer nas três perguntas: "seguir com a opção mais simples, sem adicionar dependências". Interpretação aplicada:  1. **Email:** regex simples inline, sem lib. Exige `local@domínio.tld`    (`^[^\s@]+@[^\s@]+\.[^\s@]+$`). O test antigo de `"a@b"` é atualizado    para refletir a nova regra. 2. **Campo `valid` / onde validar:** remover `valid` do user e manter    `createUser` sem validar. A validação fica só em `validateUser`, chamado    pelo handler do `POST /users`. Sem `ValidationError` no domínio. 3. **Formato dos erros:** `{ "errors": ["name is required", ...] }`, lista    de strings, cada uma começando pelo nome do campo para o client    conseguir identificar. |
| B | true | 0 | [] | - Renamed `isValidEmail` -> `emailLooksValid` in src/validate.js, src/users.js, test/users.test.js (including the test's description string). All 4 tests pass. |
| C | true | 1 | [true] | - **Limite e algoritmo** (2026-09-24): engenheiro pediu "a opção mais simples, sem adicionar dependências". Seguimos com fixed window, 10 requests por minuto por IP, in-memory, configurável via `RATE_LIMIT` e `RATE_LIMIT_WINDOW_MS`. - **Key**: `req.socket.remoteAddress` direto. Sem suporte a `x-forwarded-for` por enquanto (mais simples; adicionar quando houver proxy na frente). |

## Time: wall vs speaking (190 wpm ≈ 16 chars/s)

| Run | Wall s | Spoken chars (narrate + show notes + ask questions) | Speak s | Speak % of wall |
| --- | --- | --- | --- | --- |
| A | 223 | 2037 | 127 | 57 |
| B | 38 | 243 | 15 | 40 |
| C | 186 | 1336 | 84 | 45 |
| ALL | 447 |  | 226 | 51 |

Note: the fake `narrate` returns instantly, so wall time here excludes speaking; in the real product the speak time is added on top (narrate blocks).

## Run A — ordered event log (from calls.jsonl)

Tool sequence from stream-json: `ToolSearch → Bash → Read → Read → Read → Read → Read → Read → Read → Read → pair:narrate → Write → pair:show → pair:ask → pair:show → pair:ask → pair:show → pair:ask → pair:narrate → Edit → Write → pair:narrate → Edit → Edit → pair:narrate → Edit → pair:show → pair:narrate → Write → Edit → Edit → Bash → pair:narrate → Edit → Bash`

| t (s) | tool | file / lines | chars | text (verbatim) |
| --- | --- | --- | --- | --- |
| 0 | narrate |  | 155 | Li o projeto inteiro. Antes de mexer no código, vou escrever no PAIRING.md o plano da validação do POST de users e três dúvidas que quero alinhar com você. |
| 19 | Write | PAIRING.md |  |  |
| 24.6 | show | src/validate.js:1-3 | 0 |  |
| 26.3 | ask |  | 279 | Primeira dúvida, sobre o email. Hoje o isValidEmail aceita qualquer string com arroba, e tem um test garantindo que "a arroba b" é válido. Posso endurecer para exigir ponto no domínio, tipo local arroba domínio ponto tld, e atualizar esse test? Ou você prefere manter permissivo? → answer: Pode seguir com a opção mais simples, sem adicionar dependências. |
| 33.9 | show | src/users.js:6-10 | 0 |  |
| 35.4 | ask |  | 308 | Segunda dúvida. Com a validação no handler, o campo valid do user vira sempre true. Minha proposta é remover esse campo e deixar o createUser sem validar, já que a borda HTTP é a única porta de entrada. A alternativa seria o createUser lançar um ValidationError e o server mapear para 422. Qual você prefere? → answer: Pode seguir com a opção mais simples, sem adicionar dependências. |
| 39.3 | show | src/server.js:28-37 | 0 |  |
| 40.9 | ask |  | 224 | Terceira e última dúvida. Para a resposta 422, pensei em errors como lista de objetos com field e message, para o client conseguir marcar o campo certo. A alternativa é uma lista de strings simples. Pode ser field e message? → answer: Pode seguir com a opção mais simples, sem adicionar dependências. |
| 57.1 | narrate |  | 263 | Anotei as três decisões no PAIRING.md. Agora vou começar pelo validate.js: endurecer o isValidEmail com um regex simples que exige ponto no domínio, e criar um validateUser puro que devolve a lista de erros como strings, tratando body que não é objeto como vazio. |
| 63.9 | Edit | PAIRING.md |  |  |
| 68.5 | Write | src/validate.js |  |  |
| 72 | narrate |  | 224 | Agora no server.js. No handler do POST de users, vou chamar o validateUser antes do createUser e, se vier erro, responder 422 com a lista. Também vou passar para o createUser só o name com trim e o email, em vez do body cru. |
| 77.6 | Edit | src/server.js |  |  |
| 79.8 | Edit | src/server.js |  |  |
| 80.8 | narrate |  | 170 | Em seguida, no users.js, vou tirar o campo valid do user, porque depois da validação na borda ele seria sempre true e o import do isValidEmail deixa de fazer sentido ali. |
| 86.2 | Edit | src/users.js |  |  |
| 87 | show | test/server.test.js:12-21 | 0 |  |
| 89.2 | narrate |  | 304 | Agora os tests. Vou criar um validate test novo cobrindo os limites de tamanho do name, whitespace, emails ruins e body não objeto. No server test, adiciono os casos de 422 e confirmo que o 400 de JSON inválido continua. No users test, atualizo o caso do email a arroba b, que agora passa a ser inválido. |
| 111.2 | Write | test/validate.test.js |  |  |
| 123 | Edit | test/server.test.js |  |  |
| 127.2 | Edit | test/users.test.js |  |  |
| 136 | narrate |  | 110 | Suite completa passando, vinte e um tests. Vou só registrar o resultado no PAIRING.md e conferir o diff final. |
| 139.6 | Edit | PAIRING.md |  |  |

### Run A — every narrate text verbatim

1. (155 chars) Li o projeto inteiro. Antes de mexer no código, vou escrever no PAIRING.md o plano da validação do POST de users e três dúvidas que quero alinhar com você.
2. (263 chars) Anotei as três decisões no PAIRING.md. Agora vou começar pelo validate.js: endurecer o isValidEmail com um regex simples que exige ponto no domínio, e criar um validateUser puro que devolve a lista de erros como strings, tratando body que não é objeto como vazio.
3. (224 chars) Agora no server.js. No handler do POST de users, vou chamar o validateUser antes do createUser e, se vier erro, responder 422 com a lista. Também vou passar para o createUser só o name com trim e o email, em vez do body cru.
4. (170 chars) Em seguida, no users.js, vou tirar o campo valid do user, porque depois da validação na borda ele seria sempre true e o import do isValidEmail deixa de fazer sentido ali.
5. (304 chars) Agora os tests. Vou criar um validate test novo cobrindo os limites de tamanho do name, whitespace, emails ruins e body não objeto. No server test, adiciono os casos de 422 e confirmo que o 400 de JSON inválido continua. No users test, atualizo o caso do email a arroba b, que agora passa a ser inválido.
6. (110 chars) Suite completa passando, vinte e um tests. Vou só registrar o resultado no PAIRING.md e conferir o diff final.

Narrations not followed by a code edit before the next narration: 2

### Run A — final PAIRING.md

```markdown
# Pairing scratchpad

## Task

Adicionar input validation no caminho de criação de user (`POST /users`):

- `name` obrigatório, 2–50 chars
- `email` precisa parecer um email
- Em caso de erro, responder `422` com uma lista JSON de erros
- Adicionar tests

## Plan

1. `src/validate.js`
   - Endurecer `isValidEmail` (hoje aceita qualquer string com `@`).
   - Criar `validateUser(input)` puro, que devolve um array de erros
     (vazio quando o input é válido). Trata body que não é objeto
     (`null`, array, string) como vazio, para não estourar no destructuring.
   - `name`: trim antes de medir; vazio/ausente → "required"; fora de 2–50 → "length".
   - `email`: ausente → "required"; formato ruim → "invalid".
2. `src/server.js`
   - No handler do `POST /users`, chamar `validateUser(body)` antes de
     `createUser`. Se houver erros, `send(res, 422, { errors })` e parar.
   - Passar para `createUser` só `name` (trimmed) e `email`, não o body cru.
3. `src/users.js`
   - O campo `valid` vira sempre `true` depois da validação na borda.
     Proposta: remover (ver Open questions).
4. Tests
   - `test/validate.test.js` (novo): unit tests de `validateUser` e do
     `isValidEmail` mais estrito (limites 1/2/50/51 chars, whitespace-only,
     email sem `@`, sem domínio, com espaço, múltiplos erros de uma vez).
   - `test/server.test.js`: casos `422` (name faltando, name curto, email
     inválido, ambos errados numa só resposta com dois erros), e confirmar
     que `400` de JSON inválido continua igual.
   - Ajustar `test/users.test.js` se `valid` for removido.

## Decisions

Resposta do engineer nas três perguntas: "seguir com a opção mais simples,
sem adicionar dependências". Interpretação aplicada:

1. **Email:** regex simples inline, sem lib. Exige `local@domínio.tld`
   (`^[^\s@]+@[^\s@]+\.[^\s@]+$`). O test antigo de `"a@b"` é atualizado
   para refletir a nova regra.
2. **Campo `valid` / onde validar:** remover `valid` do user e manter
   `createUser` sem validar. A validação fica só em `validateUser`, chamado
   pelo handler do `POST /users`. Sem `ValidationError` no domínio.
3. **Formato dos erros:** `{ "errors": ["name is required", ...] }`, lista
   de strings, cada uma começando pelo nome do campo para o client
   conseguir identificar.

## Open questions

1. **Rigor do email.** Hoje `isValidEmail("a@b")` é `true` e existe um test
   garantindo isso. "Parecer um email" normalmente é `local@domínio.tld`
   (regex simples `^[^\s@]+@[^\s@]+\.[^\s@]+$`). Posso quebrar/atualizar esse
   test e exigir o ponto no domínio?
2. **Campo `valid` no user e onde validar.** Com a validação no handler,
   `valid` passa a ser sempre `true`. Proposta: remover o campo e deixar
   `createUser` sem validar (a borda HTTP é a única porta de entrada).
   Alternativa: `createUser` lança um `ValidationError` e o server mapeia
   para `422`, protegendo o domínio de qualquer caller.
3. **Formato de cada erro na lista.** Proposta:
   `{ "errors": [{ "field": "name", "message": "..." }] }`.
   Alternativa mais simples: `{ "errors": ["name is required"] }`.

## Notes

- Body não-objeto (`[]`, `null`, `"x"`) hoje passa direto para `createUser`
  e `null` estoura no destructuring. `validateUser` vai cobrir isso e
  devolver os erros de `required`.
- `readJson` já devolve `400 invalid json` para JSON quebrado; mantido.
- Resultado: `npm test` → 21 tests, 0 falhas (2 já existiam, 19 novos).
- O test antigo `isValidEmail("a@b") === true` foi removido de
  `test/users.test.js` e substituído pela cobertura em
  `test/validate.test.js` com a regra nova.
```

## Run B — ordered event log (from calls.jsonl)

Tool sequence from stream-json: `Grep → Read → Read → Read → Write → ToolSearch → pair:show → pair:narrate → Edit → Edit → Edit → Edit → Edit → Bash → Grep → pair:narrate → Edit`

| t (s) | tool | file / lines | chars | text (verbatim) |
| --- | --- | --- | --- | --- |
| 0 | Write | PAIRING.md |  |  |
| 3.9 | show | src/validate.js:1-3 | 44 | Aqui está o isValidEmail que vamos renomear. |
| 5.3 | narrate |  | 126 | Vou renomear isValidEmail para emailLooksValid nos três arquivos: validate.js, users.js e no teste, incluindo o nome do teste. |
| 7.3 | Edit | src/validate.js |  |  |
| 8.6 | Edit | src/users.js |  |  |
| 9.7 | Edit | src/users.js |  |  |
| 11 | Edit | test/users.test.js |  |  |
| 12.7 | Edit | test/users.test.js |  |  |
| 19.1 | narrate |  | 73 | Pronto, renomeei em todo o código e os testes passaram, quatro de quatro. |
| 22.9 | Edit | PAIRING.md |  |  |

### Run B — every narrate text verbatim

1. (126 chars) Vou renomear isValidEmail para emailLooksValid nos três arquivos: validate.js, users.js e no teste, incluindo o nome do teste.
2. (73 chars) Pronto, renomeei em todo o código e os testes passaram, quatro de quatro.

Narrations not followed by a code edit before the next narration: 1

### Run B — final PAIRING.md

```markdown
# PAIRING.md

## Plan: rename `isValidEmail` -> `emailLooksValid`

- `src/validate.js`: rename the exported function.
- `src/users.js`: update import and call site.
- `test/users.test.js`: update import, and the test name/calls (test name says "isValidEmail accepts..." -> should say "emailLooksValid accepts...").

## Open questions
- None — this is a mechanical rename, no ambiguity.

## Decisions
- Renamed `isValidEmail` -> `emailLooksValid` in src/validate.js, src/users.js, test/users.test.js (including the test's description string). All 4 tests pass.
```

## Run C — ordered event log (from calls.jsonl)

Tool sequence from stream-json: `ToolSearch → Glob → Read → Read → Read → Read → Read → Read → Read → Read → Read → pair:narrate → pair:show → Write → pair:ask → pair:narrate → Edit → Write → pair:narrate → Edit → Edit → Edit → Edit → pair:narrate → Write → Edit → Edit → Bash → pair:narrate → Edit → Edit`

| t (s) | tool | file / lines | chars | text (verbatim) |
| --- | --- | --- | --- | --- |
| 0 | narrate |  | 187 | Vamos adicionar rate limiting no endpoint de criação de usuários. Primeiro vou escrever o plano e as dúvidas no PAIRING.md, e depois te pergunto sobre os limites antes de mexer no código. |
| 0.7 | show | src/server.js:28-37 | 0 |  |
| 12.3 | Write | PAIRING.md |  |  |
| 21 | ask |  | 178 | Para o rate limit do POST users, eu sugiro fixed window com dez requests por minuto por client IP, configurável por env. Esses números te parecem bons, ou você quer outro limite? → answer: Pode seguir com a opção mais simples, sem adicionar dependências. |
| 31.4 | narrate |  | 218 | Beleza, vou registrar a decisão no PAIRING.md e criar o módulo rateLimit com um fixed window in-memory, keyed pelo IP do socket. Ele expõe check e reset, e limpa entradas expiradas a cada chamada pra não vazar memória. |
| 37.3 | Edit | PAIRING.md |  |  |
| 43.2 | Write | src/rateLimit.js |  |  |
| 73.2 | narrate |  | 317 | Agora vou ligar o limiter no server. No branch do POST users, antes de ler o body, checo o IP do socket e, se estourou o limite, respondo quatro dois nove com o header retry-after em segundos. Os limites vêm de env com default de dez por minuto, e vou expor o limiter e sua config pra gente resetar e usar nos testes. |
| 76.2 | Edit | src/rateLimit.js |  |  |
| 79.6 | Edit | src/server.js |  |  |
| 81.9 | Edit | src/server.js |  |  |
| 84.7 | Edit | src/server.js |  |  |
| 89.4 | narrate |  | 279 | Agora os testes. Vou criar um unit test do limiter com um clock fake pra cobrir o limite, a liberação após a janela e keys independentes, e no teste do server vou bater o limite do POST users e conferir o quatro dois nove com retry-after, além de resetar o limiter no beforeEach. |
| 101.2 | Write | test/rateLimit.test.js |  |  |
| 105.2 | Edit | test/server.test.js |  |  |
| 110.7 | Edit | test/server.test.js |  |  |
| 121.8 | narrate |  | 157 | Suite toda verde, treze testes passando. Pra fechar, vou anotar o resultado no PAIRING.md e documentar as duas variáveis de ambiente do rate limit no README. |
| 128 | Edit | PAIRING.md |  |  |
| 131.3 | Edit | README.md |  |  |

### Run C — every narrate text verbatim

1. (187 chars) Vamos adicionar rate limiting no endpoint de criação de usuários. Primeiro vou escrever o plano e as dúvidas no PAIRING.md, e depois te pergunto sobre os limites antes de mexer no código.
2. (218 chars) Beleza, vou registrar a decisão no PAIRING.md e criar o módulo rateLimit com um fixed window in-memory, keyed pelo IP do socket. Ele expõe check e reset, e limpa entradas expiradas a cada chamada pra não vazar memória.
3. (317 chars) Agora vou ligar o limiter no server. No branch do POST users, antes de ler o body, checo o IP do socket e, se estourou o limite, respondo quatro dois nove com o header retry-after em segundos. Os limites vêm de env com default de dez por minuto, e vou expor o limiter e sua config pra gente resetar e usar nos testes.
4. (279 chars) Agora os testes. Vou criar um unit test do limiter com um clock fake pra cobrir o limite, a liberação após a janela e keys independentes, e no teste do server vou bater o limite do POST users e conferir o quatro dois nove com retry-after, além de resetar o limiter no beforeEach.
5. (157 chars) Suite toda verde, treze testes passando. Pra fechar, vou anotar o resultado no PAIRING.md e documentar as duas variáveis de ambiente do rate limit no README.

Narrations not followed by a code edit before the next narration: 1

### Run C — final PAIRING.md

```markdown
# Pairing scratchpad

## Task

Adicionar rate limiting ao endpoint de criação de usuários (`POST /users`).

## Plan

1. Criar `src/rateLimit.js` com um limiter in-memory (sem dependências), keyed por client IP.
   - Fixed window: N requests por janela de X ms. Simples, previsível, suficiente para uma API tiny.
   - Expor `createRateLimiter({ limit, windowMs })` que retorna `{ check(key), reset() }`.
   - `check` devolve `{ allowed, retryAfterMs }` para o server montar o header `retry-after`.
   - Limpar entradas expiradas lazily em cada `check` para não vazar memória.
2. Em `src/server.js`, aplicar o limiter apenas no branch `POST /users`, antes de ler o body.
   - Bloqueado → `429 { error: "too many requests" }` + header `retry-after` (segundos).
   - Limites configuráveis via env (`RATE_LIMIT`, `RATE_LIMIT_WINDOW_MS`) com defaults sensatos.
3. Testes:
   - `test/rateLimit.test.js`: unit tests do limiter (permite até N, bloqueia N+1, libera após a janela, keys independentes).
   - `test/server.test.js`: teste de integração que bate o limite no `POST /users` e recebe 429; `GET /users` não é afetado.
   - Resetar o limiter no `beforeEach` junto com `resetUsers`.

## Decisions

- **Limite e algoritmo** (2026-09-24): engenheiro pediu "a opção mais simples, sem adicionar dependências". Seguimos com fixed window, 10 requests por minuto por IP, in-memory, configurável via `RATE_LIMIT` e `RATE_LIMIT_WINDOW_MS`.
- **Key**: `req.socket.remoteAddress` direto. Sem suporte a `x-forwarded-for` por enquanto (mais simples; adicionar quando houver proxy na frente).

## Open questions

- (nenhuma pendente)

## Notes

- Implementado em `src/rateLimit.js` (limiter) e `src/server.js` (aplicado só no `POST /users`).
- Resposta bloqueada: `429 { "error": "too many requests" }` + `retry-after` em segundos (mínimo 1).
- O limiter faz `req.resume()` no request bloqueado para descartar o body não lido e manter keep-alive saudável.
- `postUsersLimiter` é exportado do server (com `limit`/`windowMs`) para os testes resetarem e lerem a config.
- `npm test`: 13 testes, todos passando (2026-09-24).
- Follow-ups possíveis: suporte a `x-forwarded-for` quando houver proxy; header `x-ratelimit-remaining` nas respostas permitidas.
```

## Raw analysis JSON

See `analysis.json` next to this file.

## Interpretation

### Was the 400-char cap ever binding?

No. 0 of 13 narrations exceeded 400 chars; the longest was 317 (p90 304, median 187, mean 199). On Fable 5.1 (the session default, runs A and C) narrations centred around 200 chars (~12 s of speech at 16 chars/s); on Sonnet 5 (run B) they were 73-126 chars. So 400 is a safe hard ceiling that natural output never reaches, and the fake server never had to reject anything. What it does *not* do is keep pacing tight: 4/13 narrations were over 250 chars (16-19 s of speech each) and 3/13 had 3-4 sentences, breaking the "1-2 sentences" rule. The lever that controls length is the prompt wording, not the cap.

A tighter cap would have been actively harmful: 300 would have rejected 2/13 and 250 would have rejected 4/13 (31%), each costing a wasted round-trip and a re-phrased narration. Both >300 narrations were the legitimately dense "now the tests, three files" batch narrations.

### Did "narrate before every file edit" happen?

Per Edit/Write call: no. 13 narrations for 21 code edits (0.62 per code edit); 16 of 29 edit calls had no narration since the previous edit. Per logical change: yes, 9/9 code-edit batches in all three runs were preceded by a narration that named the files about to change (one narration typically covered 1.5 files and ~2 edit calls; run B's rename covered 3 files / 5 edits with one 126-char narration). This is the model reading "before every file edit" as "before every change", and it is the better behaviour for pacing; the PRD should say so explicitly rather than get it by accident. The only un-mentioned edit was a small follow-up tweak to `rateLimit.js` in run C under the "wire the limiter into the server" narration.

### ask / show / PAIRING.md

- `ask`: used in both Fable runs (A: 3 asks, C: 1); never in the Sonnet rename (correctly, nothing ambiguous). Run A asked three questions back-to-back before any code edit; at 224-308 chars each they are *longer* than the median narration and would be ~51 s of TTS plus three record/transcribe cycles before coding starts. The PRD has no cap on `ask`. Every ask was followed by a Decisions entry in PAIRING.md (4/4), and the canned "simplest option, no deps" answer was interpreted sensibly each time.
- `show`: 6 calls; 5 had no `note` and were followed within 0.7-2 s by an `ask` or `narrate` that explained the pointer. Auto-narrating a default line on empty note (open question in the PRD) would therefore be redundant speech.
- PAIRING.md: written before the first code edit in 3/3 runs; Plan and Open questions filled; Decisions appended after every ask; all runs added a closing Notes/result entry. Sonnet wrote it in English, Fable in Portuguese.
- Language/format: 13/13 narrations in Portuguese with English technical terms (endpoint, handler, fixed window, regex, header retry-after); 0/13 contained markdown, backticks or code. Unprompted TTS-friendly phrasing: "quatro dois nove" for 429, "a arroba b" for `a@b`, "domínio ponto tld".
- The typed final summaries (A: 1743 chars, C: 1543 chars, markdown) are never spoken; in a voice session the engineer only hears the 110-157-char wrap-up narration. B's typed summary was in English.

### Pacing cost

At 190 wpm, narrations + show notes + ask questions would add 127 s to run A's 223 s (57%), 84 s to C's 186 s (45%) and 15 s to B's 38 s (40%). Since `narrate`/`ask` block, real sessions are ~1.4-1.6x the wall time measured here, and roughly a third of that time is speech. Ask questions are 40% of A's spoken chars.

### Environment notes

- Claude Code 2.1.282 defers MCP tool schemas: every run spent its first tool call on `ToolSearch select:mcp__pair__...` before it could call `narrate`. It worked 3/3 times with the CLAUDE.md block naming the tools, but it is one more turn and one more thing that can silently not happen.
- `env -u CLAUDECODE` was enough to launch nested sessions; `CLAUDE_CODE_ENTRYPOINT` did not need unsetting. macOS lacks `timeout`; a perl `alarm` shim was used. Add `< /dev/null` to `claude -p` runs to avoid the 3 s stdin wait.
- Nothing outside the scratch dir was changed by me: no `claude mcp add`, `~/.claude/settings.json` untouched (mtime predates the runs), `~/.claude.json` contains no `pair` server entry (its mtime moved because Claude Code records per-project session state for any cwd it runs in).

## Verdict

**VERIFIED** for the value: a 400-char hard reject in `narrate` is right as a ceiling (never hit in 13 natural narrations, max 317) and tightening it would only cause retries. **But** the PRD's premise that "the prompt rules plus the 400-character cap" keep pacing tight is not borne out: the cap is not binding, the 1-2 sentence rule was exceeded in 3/13 narrations, and speech would be ~45-57% on top of wall time. The controls that need changing are in the prompt and in `ask`, not in the number.

### Concrete PRD changes

1. Keep `narrate` cap at 400. Apply the same 400-char reject to `ask` (questions ran 178-308 chars; no cap today). State the cap in both tool descriptions ("max 400 characters; aim for under 200") so the model sees it before being rejected.
2. Pairing block, narrate rule: "Before every file edit" -> "Before each change, call `narrate` with one or two short sentences (under about 200 characters, ~10 seconds of speech): what you're changing and why. One narration may cover several related edits to the files it names." This matches observed behaviour and adds the missing length target.
3. Pairing block, ask rule: add "One `ask` per decision point, phrased in one or two sentences with your recommended option first. If you have several small questions, ask the most consequential one and write the rest under Open questions with the default you will take."
4. Pairing block: add "Write PAIRING.md in the same language you speak" (Sonnet wrote it in English) and "End the turn with one `narrate` saying what changed and what to look at" (the long typed summary is invisible in a voice session).
5. Open question "should `show` auto-narrate when note is empty": answer no; 5/6 empty-note shows were followed within 2 s by speech. Play a short tick at most.
6. Component 2 / README: document that Claude Code 2.1.x defers MCP tool schemas and the model loads them via `ToolSearch` on first use; `pair doctor` could run a one-turn probe like the smoke test here (`claude -p "call narrate with 'smoke test'"`) to confirm the tools are reachable.
7. Risks table: replace "character cap" as a mitigation for "narrates too much" with "length target in prompt + `Stop` hook logging spoken chars and speech seconds per turn"; add "`ask` questions are as long as narrations and each costs speak + record + transcribe; cap and phrasing rule above".
