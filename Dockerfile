# All-in-one CoordWorks image: control plane + web UI + embedded runner.
# Build:  docker build -t coordworks .
# Run:    docker run -p 8080:8080 -e COORDWORKS_RUNNER_TOKEN=$(openssl rand -hex 16) \
#           -v coordworks-data:/data coordworks
#
# For a dedicated runner image (scale-out on docker/k8s/bare metal) see
# deploy/docker/Dockerfile.runner; docker-compose.yml wires both together.

FROM node:22-alpine AS web
WORKDIR /app/web
COPY web/package.json web/package-lock.json* ./
RUN npm install
COPY web/ ./
RUN npm run build

FROM golang:1.25-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
RUN CGO_ENABLED=0 go build -o /coordworks-server ./cmd/server

FROM alpine:3.21
# CA bundle comes from the build stage (LLM APIs are HTTPS); adduser is a
# busybox builtin, so no packages need to be installed here.
RUN adduser -D coordworks && mkdir -p /data && chown coordworks /data
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
USER coordworks
WORKDIR /data
COPY --from=build /coordworks-server /usr/local/bin/coordworks-server
COPY --from=web /app/web/dist /srv/web
ENV COORDWORKS_DB=/data/coordworks.db \
    COORDWORKS_STATIC_DIR=/srv/web \
    COORDWORKS_ADDR=:8080
EXPOSE 8080
ENTRYPOINT ["coordworks-server"]
