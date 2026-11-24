.PHONY: deps build test test-integration fmt vet docker

deps:
	go mod tidy
	cd integration-tests && go mod tidy

build:
	go build -trimpath -ldflags="-s -w" -o bin/statusd ./cmd/statusd

test:
	go test ./...

test-integration:
	cd integration-tests && go test -count=1 ./...

fmt:
	test -z "$$(gofmt -l cmd internal)"

vet:
	go vet ./...

docker:
	docker build -t localhost/status:dev .
