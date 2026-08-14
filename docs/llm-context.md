# MechMind lean LLM context

MechMind runs diagnosis in Go first. The LLM (optional) only writes short bay prose from a **sparse packet**.

## Default behavior

`GET /v1/codes/{code}/explain?vin=` returns:

- full `findings`, `observations`, history (for the UI)
- `llm_packet` — the **only** JSON that would be sent to a model

No LLM call unless you ask:

```text
GET /v1/codes/P0171/explain?vin=...&narrative=1
GET /v1/codes/P0171/explain?vin=...&narrative=1&force_narrative=1
```

## What goes into `llm_packet`

| Included | How curated |
|---|---|
| Focus code | Single code |
| Vehicle | One line make/model/year if known |
| Findings | Top **3** by confidence, **2** evidence lines each |
| KB | One article: summary + ≤3 causes/tests/parts |
| Co-occur | Top **2** with rate ≥ 0.25 |
| History | Up to **2** compressed date+status lines |
| Ask | Fixed short instruction |

**Never** sent to the model: raw live PID objects, full observation dumps, recalls lists, multi-page history, chat turns.

## When MechMind skips the LLM

| Reason | Meaning |
|---|---|
| `llm_disabled` | `LLM_ENABLED` false or no API key |
| `no_findings_or_kb` | Nothing useful to narrate |
| `kb_only` | Dictionary article alone — UI is enough |
| `structured_sufficient` | Single high-confidence finding + KB |
| `forced` / `ok` | Will call (or return cache) |

Identical packets are **cached** in-process by fingerprint (24h).

## Config

```bash
LLM_ENABLED=true
LLM_API_KEY=sk-...
LLM_BASE_URL=https://api.deepseek.com
LLM_MODEL=deepseek-chat
```

OpenAI-compatible bases work (same `/chat/completions` shape). DeepSeek is the documented default; Anthropic/Gemini need a different client and are not wired.

## Design note

There is no hard token counter. Cost control is **structural**: rank → cut → gate → cache → small model → short completion (`max_tokens` on the *answer* only).
