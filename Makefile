
BINARY      := server
CMD_PKG     := ./cmd/server
BUILD_DIR   := ./bin

VERSION     := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT_SHA  := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME  := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

LDFLAGS := -ldflags="-s -w \
	-X main.version=$(VERSION) \
	-X main.commitSHA=$(COMMIT_SHA) \
	-X main.buildTime=$(BUILD_TIME)"

all: lint build

build:
	@echo "Building $(BINARY) ($(VERSION) @ $(COMMIT_SHA))"
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 go build $(LDFLAGS) -trimpath -o $(BUILD_DIR)/$(BINARY) $(CMD_PKG)
	@echo "Binary: $(BUILD_DIR)/$(BINARY)"

lint:
	@echo "Linting"
	golangci-lint run --timeout=5m ./...

fmt:
	@echo "Formatting"
	gofmt -s -w .
	goimports -local github.com/ademajagon/gopay-service -w .

vet:
	@echo "Vetting"
	go vet ./...

tidy:
	@echo "Tidying go.mod"
	go mod tidy
	@echo "Verifying module graph"
	go mod verify