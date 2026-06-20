// Package edge hosts the seasonfill REST surface. Annotations on
// this file feed swaggo/swag to emit docs/swagger.yaml — see
// Makefile `openapi`.
//
// @title           Seasonfill API
// @version         1.0
// @description     REST API for seasonfill — Sonarr season-fill orchestration.
// @description     Routes accept either X-Api-Key header or a signed
// @description     seasonfill_session cookie issued by POST /api/v1/auth/login.
// @servers.url             /api/v1
// @servers.description     Primary API base path
// @securityDefinitions.apikey  ApiKeyAuth
// @in              header
// @name            X-Api-Key
// @securityDefinitions.apikey  CookieAuth
// @in              cookie
// @name            seasonfill_session
package edge
