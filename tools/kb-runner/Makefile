.PHONY: build test e2e

BINARY := kb-runner

build:
	go build -o $(BINARY) .

test:
	go test -race -count=1 ./...

e2e:
	@echo "no e2e suite for kb-runner"
