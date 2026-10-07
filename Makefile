VERSION ?= 0.1.0
IMAGE   ?= ghcr.io/azeno/xdocker

.PHONY: all ui build test run docker spk src clean

all: test build

ui:
	cd frontend && npm ci --no-audit --no-fund && npm run build

build: ui
	cd backend && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o ../dist/xdocker ./cmd/xdocker

test:
	cd backend && go vet ./... && go test ./...
	cd frontend && npx tsc -b --noEmit

# Local run against Docker Desktop / a local daemon.
run: build
	XDOCKER_DATA_DIR=./dist/data XDOCKER_STACKS_DIR=$$PWD/dist/stacks XDOCKER_HOST_PROC= \
	XDOCKER_DOCKER_HOST=$${DOCKER_HOST:-unix://$$HOME/.docker/run/docker.sock} ./dist/xdocker

docker:
	docker build -f deploy/Dockerfile --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) -t $(IMAGE):latest .

spk:
	./spk/build.sh $(VERSION)

# Source tarball with the web UI prebuilt (for spksrc, which has no npm).
src: ui
	mkdir -p dist
	tar --exclude=node_modules --exclude=dist --exclude=.git -czf dist/xdocker-$(VERSION)-src.tar.gz \
		-s ',^,xdocker-$(VERSION)/,' backend frontend deploy spk LICENSE README.md 2>/dev/null || \
	tar --exclude=node_modules --exclude=dist --exclude=.git --transform 's,^,xdocker-$(VERSION)/,' -czf dist/xdocker-$(VERSION)-src.tar.gz \
		backend frontend deploy spk LICENSE README.md

clean:
	rm -rf dist
