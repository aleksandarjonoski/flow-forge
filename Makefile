# Builds FlowForge as one static binary with the editor built in.
# See docs/server-v1.md §8.
#
#   make build     frontend + bin/flowforge for this machine
#   make release   frontend + one binary per release target in bin/
#   make test      backend and frontend tests
#   make clean     remove build output
#
# Needs Go and Node 22 (frontend/.nvmrc). The binaries need nothing.

WEBUI_DIST := backend/internal/webui/dist
TARGETS    := linux/amd64 linux/arm64 darwin/arm64 darwin/amd64 windows/amd64
GOBUILD    := CGO_ENABLED=0 go build -trimpath -ldflags "-s -w"

.PHONY: build release frontend embed test clean

build: embed
	cd backend && $(GOBUILD) -o ../bin/flowforge ./cmd/flowforge
	@echo "built bin/flowforge"

release: embed
	@for target in $(TARGETS); do \
		os=$${target%/*}; arch=$${target#*/}; \
		out=bin/flowforge-$$os-$$arch; \
		if [ $$os = windows ]; then out=$$out.exe; fi; \
		echo "building $$out"; \
		(cd backend && GOOS=$$os GOARCH=$$arch $(GOBUILD) -o ../$$out ./cmd/flowforge) || exit 1; \
	done

frontend: frontend/node_modules/.installed
	cd frontend && npm run build

# Reinstall the frontend's packages only when the lock file changes.
frontend/node_modules/.installed: frontend/package-lock.json
	cd frontend && npm ci
	touch $@

# embed copies the editor build where go:embed picks it up, replacing any
# earlier build but keeping .gitkeep.
embed: frontend
	find $(WEBUI_DIST) -mindepth 1 ! -name .gitkeep -delete
	cp -R frontend/dist/. $(WEBUI_DIST)/

test:
	cd backend && go test ./...
	cd frontend && npm test

clean:
	rm -rf bin frontend/dist
	find $(WEBUI_DIST) -mindepth 1 ! -name .gitkeep -delete
