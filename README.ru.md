<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/logo-dark.png">
    <img src="docs/logo-light.png" alt="seasonfill" width="360">
  </picture>
</p>

# Seasonfill

Сопутствующий сервис для [Sonarr](https://sonarr.tv), автоматизирующий
повторный захват обновлённых сезонных пакетов в тех случаях, когда
встроенная upgrade-логика Sonarr отказывается это делать. Multi-instance,
работает по расписанию, принимает webhooks и замыкает петлю
import-success/failure. Небольшой React-UI для ручного вмешательства.

> **English version:** [README.md](README.md).

> **Статус проекта: alpha.** Ломающие
> изменения (схема конфига, форма values чарта, колонки БД) вероятны
> до `v1.0`. Пините теги образов в продакшене.

## Быстрый старт

Выберите путь развёртывания:

- **Docker Compose** — single-host, проще всего. См.
  [`deploy/compose/README.md`](deploy/compose/README.md).
- **Kubernetes через Helm** — продакшн / homelab-кластеры. Чарт по
  адресу `oci://ghcr.io/alexmorbo/seasonfill-helm`. См.
  [`deploy/helm/seasonfill/README.md`](deploy/helm/seasonfill/README.md).

Любой путь поднимает два контейнера (Go-backend + nginx, отдающий
SPA), единую HTTP fan-out точку на порту `8080` и SQLite по умолчанию
(Postgres опционален). На первом запуске в лог backend печатается
одноразовый admin-пароль (см. compose README для `grep`-рецепта).

## Что он делает

Sonarr не будет автоматически захватывать сезонный пакет, в котором
есть эпизоды, уже имеющиеся у вас с тем же качеством — даже если
в этом же пакете есть *дополнительные недостающие эпизоды*. См.
[Sonarr#5740](https://github.com/Sonarr/Sonarr/issues/5740),
[#6378](https://github.com/Sonarr/Sonarr/issues/6378),
[#5032](https://github.com/Sonarr/Sonarr/issues/5032). Типичный
отказ выглядит так:

```text
Existing file on disk has a equal or higher Custom Format score: 500
Full season pack
```

Ручной обходной путь — открыть Interactive Search в Sonarr и нажать
**Override and add to Download Queue** на том же релизе. Seasonfill
автоматизирует эту петлю: принимает решение по *покрытию эпизодов*
(а не по Custom Format score), ранжирует кандидатов и форсированно
захватывает лучшего через тот же endpoint, что использует UI Sonarr.
Webhook-приёмник обновляет grab-запись по факту import success/failure
и заряжает cooldown'ы, чтобы не пере-захватывать сломанные релизы.

## Возможности

| Возможность | Статус |
|-------------|--------|
| Параллельное сканирование нескольких Sonarr-инстансов | shipped |
| Расписание по cron + ручной `POST /scan` | shipped |
| `mode: auto\|manual` per-instance (manual = только UI) | shipped |
| `dry_run` per-instance (по умолчанию глобально = true) | shipped |
| Приёмник Sonarr `Connect → Webhook` (Grab/Import/ImportFailed) | shipped |
| Cooldown'ы по GUID + per-series (smart) | shipped |
| Audit-лог решений + операторский «Grab now» override | shipped |
| Операторский Rescan со связью supersession | shipped |
| React SPA: Dashboard, Instances, Scans, Decisions, Grabs | shipped |
| Auth-режимы: Forms / Basic / None / OIDC (переключаются на лету) | shipped |
| OIDC SSO с PKCE + group ACL (Keycloak / Authelia / Authentik) | shipped |
| Постоянный API-ключ для webhook'а Sonarr (`X-Api-Key`) | shipped |
| Авто-генерация пароля при первом запуске (в стиле qBittorrent) | shipped |
| Rescue-CLI `reset-password` + `auth-mode` | shipped |
| Bypass для локальных адресов (RFC1918/loopback) | shipped |
| Helm-чарт (`oci://ghcr.io/alexmorbo/seasonfill-helm`) | shipped |
| Стек Docker Compose | shipped |
| Prometheus `/metrics` + `ServiceMonitor` | shipped |
| Аниме (абсолютная нумерация) | **не поддерживается** |

## Обзор конфигурации

Bootstrap-настройки (БД, HTTP bind, log-level, ключ шифрования, admin)
приходят через переменные окружения — см.
[`deploy/compose/.env.example`](deploy/compose/.env.example) или
`values.yaml` чарта. Всё остальное (Sonarr-инстансы, расписание cron,
dry_run, лимиты, TTL сессии, trusted proxies) хранится в БД и
редактируется через Settings UI на `/settings`.

Чувствительные значения (`SEASONFILL_API_KEY`, Postgres DSN,
admin-пароль) приходят через env. Helm-чарт прокидывает их через
`valueFrom.secretKeyRef` из заранее созданного или сгенерированного
чартом Secret'а; compose — через `.env`. Точные имена ключей см.
в README соответствующего пути развёртывания.

## API

REST API под `/api/v1/*` (включая `/api/v1/auth/login`,
`/api/v1/webhook/sonarr/<instance-name>` и т. д.). Публичные пробы
`/healthz`, `/readyz`, `/metrics`. Любой не-пробный маршрут требует
либо session-cookie (UI), либо заголовок `X-Api-Key` (Sonarr-webhook,
скрипты).

OpenAPI 3.0 спека закоммичена в
[`docs/swagger.yaml`](docs/swagger.yaml). Открыть можно любым
OpenAPI-просмотрщиком (Swagger UI, Redoc, IntelliJ HTTP client) —
сам сервис live-UI для спеки не хостит.

## Модель безопасности

### Режимы аутентификации

Четыре режима, переключаются runtime через **Settings → Security**
(перезапуск не нужен):

| Режим | Случай использования | Local bypass уместен? | Нужен reverse proxy? |
|-------|----------------------|----------------------|---------------------|
| **Forms** (по умолчанию) | Прямой браузерный доступ, публичный | Опционально | Нет |
| **Basic** | CLI/скрипты, deploys с IP-ограничениями | Опционально | Рекомендуется для публичных |
| **None** | Полностью за authenticating-прокси | N/A | **Да — обязательно** |
| **OIDC** | SSO через OIDC | Опционально | Reverse proxy с TLS рекомендуется |

> **Сценарии деплоя:**
>
> | Сценарий | Рекомендация |
> |----------|--------------|
> | Локальный docker-compose, доверенный LAN | Forms + Disabled-for-Local (сеется из `.env.example`) |
> | Публичный через Pangolin / oauth2-proxy / Authelia | None + auth на reverse-proxy |
> | Публичный напрямую | Forms + сильный регулярно-меняемый пароль + HTTPS |

### Инвариант webhook'а

`POST /api/v1/webhook/sonarr/<instance-name>` всегда требует заголовок
`X-Api-Key` независимо от auth-режима и состояния local-bypass. Это
публично-доступная поверхность, для неё bypass не работает никогда.

### Другие свойства безопасности

- Один admin-пользователь (username + bcrypt-хеш пароля в БД).
  Авто-генерируемый 24-символьный пароль при первом запуске, если ни
  один не сконфигурирован — печатается один раз в логи с баннером
  `FIRST-RUN PASSWORD`. Docker Compose поставляется с `admin/admin` в
  `.env.example` — **смените при первом входе** через Settings →
  Security.
- Cookie подписан HMAC ключом API. `HttpOnly`, `SameSite=Strict`,
  флаг `Secure` опт-ин (переключается в Settings → Security при
  HTTPS). Смена режима бампит session epoch — все живые cookie
  инвалидируются мгновенно.
- API-ключ переживает рестарт. Ротация — правка Secret / `.env` +
  redeploy. Sonarr `Connect → Webhook` шлёт его как Custom header
  (`X-Api-Key`). Работает в любом auth-режиме.
- Rate-limit на `/auth/login` (5 попыток / IP / 15 мин) и `/webhook`.
  Constant-time сравнение пароля. Generic сообщение об ошибке
  («Invalid credentials») для неизвестного пользователя и неверного
  пароля.
- `GET /api/v1/instances` маскирует Sonarr `api_key` — никогда не
  возвращается ни одним read-endpoint'ом.
- Все web-ответы несут strict Content-Security-Policy плюс
  `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`,
  `Referrer-Policy`, `Permissions-Policy` (на уровне nginx).
- CI блокирует публикацию образа при vulnerabilities в зависимостях:
  `govulncheck` (Go, reachability-режим) и `npm audit` (web, high+).
  Еженедельный Dependabot держит зависимости актуальными.

См. [`deploy/compose/README.md`](deploy/compose/README.md) для
деталей runtime-конфигурации и команды rescue при lockout.

### Настройка OIDC (пример с Keycloak)

1. Деплой с `oidc.enabled=true` (Helm) или `OIDC_CLIENT_SECRET=...` (compose).
2. В Keycloak создайте:
   - Realm (например, `homelab`)
   - Client (например, `seasonfill`) с:
     - Access Type: confidential
     - Standard Flow Enabled
     - Valid Redirect URIs: `https://<host>/api/v1/auth/oidc/callback`
     - Web Origins: `https://<host>`
   - Скопируйте client secret → `OIDC_CLIENT_SECRET` env у Seasonfill
3. В Seasonfill: Settings → Security → Authentication: `OIDC`. Заполните:
   - Issuer URL: `https://<keycloak-host>/realms/homelab`
   - Client ID: `seasonfill`
   - Redirect URL: `https://<host>/api/v1/auth/oidc/callback`
   - Scopes: `openid`, `profile`, `email`
   - Username claim: `preferred_username` (по умолчанию; совпадает с дефолтом Keycloak)
   - Allowed groups: опционально; пусто = «любой верифицированный пользователь»
4. Save. Все живые cookie инвалидируются (бамп session epoch).
   Перелогиньтесь через SSO-кнопку на странице логина.

### Восстановление доступа

Если заблокировались, выполните из shell контейнера:

```
seasonfill auth-mode --set forms
```

Это вернёт forms-auth без сброса OIDC-конфига — можно починить
проблему и переключиться обратно.

## Contributing

PR'ы приветствуются. Кодовая база под GPL-3.0 — можно форкать,
запускать, модифицировать. Баги и обсуждение фич — заводите
[GitHub Issue](https://github.com/alexmorbo/seasonfill/issues); для
правок кода — открывайте pull request в `main`.

## License

[GPL-3.0](LICENSE). Форки и производные работы должны оставаться
open-source под совместимой лицензией.
