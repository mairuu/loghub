# The UI, built here and served by Caddy at the edge (ADR 0005, ADR 0006).
# Keep the Node version in step with NODE_IMAGE in the Makefile.
FROM node:24-alpine AS build
WORKDIR /src
COPY package.json package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci --no-audit --no-fund
COPY . .
RUN npm run build

FROM caddy:2.11.4-alpine
# Caddy binds 80 and 443 through the file capability the image gives it, so
# it needs no root. Named volumes take their owner from these directories.
RUN addgroup -S caddy && adduser -S -G caddy -H -s /sbin/nologin caddy \
    && chown caddy:caddy /data /config
COPY Caddyfile /etc/caddy/Caddyfile
COPY --from=build /src/dist /srv
USER caddy
