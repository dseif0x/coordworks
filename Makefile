.PHONY: all build server runner web dev test clean

all: build

## build: compile server + runner binaries and the web UI
build: web
	go build -o bin/coordworks-server ./cmd/server
	go build -o bin/coordworks-runner ./cmd/runner

server:
	go build -o bin/coordworks-server ./cmd/server

runner:
	go build -o bin/coordworks-runner ./cmd/runner

web:
	cd web && npm install && npm run build

## dev: run the control plane (expects web/dist to exist; use `make web` once,
## or run `cd web && npm run dev` for a hot-reloading UI on :5173)
dev:
	COORDWORKS_RUNNER_TOKEN=$${COORDWORKS_RUNNER_TOKEN:-dev-token} go run ./cmd/server

test:
	go test ./...

clean:
	rm -rf bin web/dist
