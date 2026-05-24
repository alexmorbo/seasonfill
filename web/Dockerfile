# syntax=docker/dockerfile:1.7

ARG NODE_VERSION=20
ARG NGINX_VERSION=1.27
ARG VITE_APP_VERSION=dev
ARG GIT_SHA=unknown

# ---------------------------------------------------------------------
# Stage 1 — build the Vite bundle.
# ---------------------------------------------------------------------
FROM node:${NODE_VERSION}-alpine AS build

WORKDIR /app

# Copy lockfile first so `npm ci` is cacheable across source-only edits.
COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund --prefer-offline

# Copy the rest of the SPA source.
COPY . .

ARG VITE_APP_VERSION
ENV VITE_APP_VERSION=${VITE_APP_VERSION}

# Production build. Drops sourcemaps via vite.config.ts build settings
# (sourcemap: true today — story 009f1 may flip to false for prod).
RUN npm run build

# ---------------------------------------------------------------------
# Stage 2 — serve the bundle via nginx-unprivileged.
#
# nginx-unprivileged listens on 8080 by default, runs as UID 101
# (nginx), and ships with /tmp + /var/cache/nginx + /var/run owned by
# that UID — PSS-Restricted compatible without USER/setcap gymnastics.
# ---------------------------------------------------------------------
FROM nginxinc/nginx-unprivileged:${NGINX_VERSION}-alpine

ARG VITE_APP_VERSION
ARG GIT_SHA

LABEL org.opencontainers.image.title="seasonfill-web" \
      org.opencontainers.image.description="Seasonfill web UI — React SPA served by nginx" \
      org.opencontainers.image.source="https://github.com/alexmorbo/seasonfill" \
      org.opencontainers.image.url="https://github.com/alexmorbo/seasonfill" \
      org.opencontainers.image.licenses="GPL-3.0-or-later" \
      org.opencontainers.image.vendor="AlexMorbo" \
      org.opencontainers.image.version="${VITE_APP_VERSION}" \
      org.opencontainers.image.revision="${GIT_SHA}"

# `gettext` provides `envsubst`, used by entrypoint.sh to render the
# nginx template. Stay on root for the install + COPY, drop back to UID
# 101 for runtime.
USER root
RUN apk add --no-cache gettext \
    && rm -f /etc/nginx/conf.d/default.conf \
    && mkdir -p /etc/nginx/templates

# Template is rendered at container start (see entrypoint.sh).
COPY nginx.conf.template /etc/nginx/templates/seasonfill.conf.template
COPY --chmod=0755 entrypoint.sh /entrypoint.sh

USER 101

# Static bundle.
COPY --from=build --chown=101:101 /app/dist /usr/share/nginx/html

EXPOSE 8080

ENTRYPOINT ["/entrypoint.sh"]
