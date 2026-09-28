FROM node:24-alpine AS frontend
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
ENV NODE_OPTIONS=--max-old-space-size=384
RUN npm run build

FROM golang:1.26-alpine AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY --from=frontend /src/frontend/dist /frontend-dist
COPY cmd/ cmd/
COPY internal/ internal/
ARG VERSION=development
ENV CGO_ENABLED=0 GOMAXPROCS=2 GOMEMLIMIT=512MiB
RUN go build -p=2 -trimpath -ldflags='-s -w' -o /server ./cmd/server
RUN go build -p=2 -trimpath -ldflags='-s -w' -o /patterns-maintenance ./cmd/patterns-maintenance

FROM alpine:3.23
COPY deploy/certs/sectigo-public-server-authentication-ca-ov-r36.crt /usr/local/share/ca-certificates/
RUN apk add --no-cache ca-certificates tzdata && apk add --no-cache --virtual .certificate-validation openssl \
    && openssl verify -CAfile /etc/ssl/certs/ca-certificates.crt /usr/local/share/ca-certificates/sectigo-public-server-authentication-ca-ov-r36.crt \
    && update-ca-certificates && apk del .certificate-validation && adduser -D -u 10001 dashboard
WORKDIR /app
COPY --from=backend /server /app/server
COPY --from=backend /patterns-maintenance /app/patterns-maintenance
COPY --from=backend /frontend-dist /app/frontend/dist
ARG VERSION=development
LABEL org.opencontainers.image.source="https://github.com/rtfpessoa/lisboa-publica" \
      org.opencontainers.image.revision=$VERSION
RUN mkdir -p /app/transport-history && chown dashboard:dashboard /app/transport-history
USER dashboard
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=90s --retries=3 \
    CMD wget -q -T 4 -O /dev/null http://127.0.0.1:8080/api/v1/health || exit 1
ENTRYPOINT ["/app/server"]
