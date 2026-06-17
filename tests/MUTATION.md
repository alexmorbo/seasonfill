# Mutation Testing

Seasonfill uses [go-gremlins/gremlins](https://github.com/go-gremlins/gremlins) v0.6.0 to verify **test efficacy** — насколько тесты реально ловят регрессии.

## What is mutation testing

Gremlins берёт source code (только `domain/` + `application/`, см. `.gremlins.yaml`), вносит маленькие изменения (mutants): `>` → `>=`, `+` → `-`, `++` → `--`, инвертирует boolean. Затем перезапускает relevant tests. Classification:

- **KILLED** — тест упал на mutant. Хорошо.
- **LIVED** — тест прошёл на mutated коде. Плохо — слепой к этой ветке.
- **NOT COVERED** — line coverage = 0.
- **NOT VIABLE** — mutated код не компилируется.
- **TIMED OUT** — тесты зависли.

**Test efficacy** = `KILLED / (KILLED + LIVED)`. Высокая efficacy = тесты с зубами.

## How to read report

Workflow `Mutation Testing` (`.github/workflows/mutation-nightly.yml`) запускается каждое воскресенье 04:00 UTC и публикует `mutation-report.json` artifact (retention 90 дней).

```bash
gh run list --workflow="Mutation Testing" --limit 1
gh run download <run-id> -n mutation-report
jq '.test_efficacy, .mutants_killed, .mutants_lived' mutation-report.json
```

Локальный run:

```bash
go install github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0
# Full domain+application (~2-3 min локально)
gremlins unleash --output mutation-report.json
# Dry-run (без выполнения тестов)
gremlins unleash --dry-run
```

> Примечание про `-E` CLI flag: `gremlins unleash -E '<regex>'` **полностью заменяет** `exclude-files` из `.gremlins.yaml`, поэтому web/node_modules/.go начинает попадать в скоуп. Для частичных запусков лучше править config или `cd` в подпакет.

## Baseline

| Date       | Git SHA   | Scope                     | Efficacy | Coverage | Killed / Total | Notes                                                                                          |
| ---------- | --------- | ------------------------- | -------- | -------- | -------------- | ---------------------------------------------------------------------------------------------- |
| 2026-06-17 | `02395f2` | `domain/` + `application/` | 100.00%  | 89.93%   | 1830 / 1830    | First baseline. 205 mutants `NOT COVERED` (10.07%). 0 `LIVED`. Default mutators only. Elapsed ~3 min локально. |

> Threshold в `.gremlins.yaml` сейчас `efficacy: 40`. Этот floor оставлен низким, чтобы изменения в скоупе (vertical slicing Phase 1+, новые adapter pulls) не ломали nightly. По мере стабилизации скоупа подтягиваем floor — см. Roadmap ниже.

## Roadmap

- **F-5e (this story)** — baseline 100% efficacy / 89.93% coverage установлен. Threshold `efficacy: 40` (с запасом). Дефолтные mutators: arithmetic-base, conditionals-boundary, conditionals-negation, increment-decrement, invert-negatives.
- **Phase 1+** — после vertical slicing подтянуть `efficacy: 70` и закрыть `NOT COVERED` участки в `domain/series/` и `domain/regrab/`. Включить off-by-default mutators (arithmetic-assignments, conditionals-binaries-removal).
- **Phase 2+** — расширить scope на `infrastructure/sonarr/` + `infrastructure/tmdb/` decoder logic.
- **Long-term** — Diff mode per-PR job для critical files.

## Out of scope

- Mutation testing для `infrastructure/`, `interface/`, `cmd/` — low value на adapters.
- Custom mutators — gremlins не поддерживает plugin API.
