.PHONY: test lint vuln coverage build vet

test:
	go test -race ./...

lint:
	golangci-lint run

vuln:
	govulncheck ./...

coverage:
	go test -coverprofile=coverage.out ./... && go tool cover -html=coverage.out

build:
	go build ./...

vet:
	go vet ./...
