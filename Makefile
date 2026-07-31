BINARY := pmx
BUILD_DIR := $(CURDIR)/dist

.PHONY: all build install fmt-check vet test race check clean

all: check

build:
	@mkdir -p "$(BUILD_DIR)"
	go build -trimpath -o "$(BUILD_DIR)/$(BINARY)" ./cmd/pmx

install:
	go install ./cmd/pmx

fmt-check:
	@unformatted="$$(find cmd internal -type f -name '*.go' -exec gofmt -l {} +)"; \
	if [ -n "$$unformatted" ]; then \
		echo "Go files need formatting:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

vet:
	go vet ./...

test:
	go test ./...

race:
	go test -race ./...

check: fmt-check vet test race build

clean:
	rm -rf -- "$(BUILD_DIR)"
