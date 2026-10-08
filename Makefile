.PHONY: build test vet fmt lint run

build:
	mkdir -p bin
	CGO_ENABLED=0 go build -o bin/edugit ./cmd/edugit

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

# lint fails if any file is unformatted.
lint: vet
	@test -z "$$(gofmt -l .)" || (gofmt -l . && echo "gofmt needed" && exit 1)

run:
	go run ./cmd/edugit
