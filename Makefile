BINARY := build/fast-racing-neo-server

.PHONY: all build run test e2e fmt vet clean

all: build

build:
	go build -o $(BINARY) .

run:
	go run .

test:
	go test ./...

# Requires the Python environment described in README.md, and a server already
# listening on the configured ports.
e2e:
	python tests/e2e_test.py

fmt:
	gofmt -w .

vet:
	go vet ./...

clean:
	rm -rf build
