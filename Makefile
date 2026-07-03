FUZZTIME ?= 10s
ACTIONLINT_VERSION ?= v1.7.7

.PHONY: test test-race test-bench test-integration test-fuzz lint-actions fmt vet lint lint-fix cover tidy

test:
	go test ./...

test-race:
	go test -race ./...

test-bench:
	go test -bench=. ./...

test-integration:
	cd integration && go mod tidy -diff && go vet ./... && go test -race -count=1 ./...

test-fuzz:
	go test . -run '^$$' -fuzz='^FuzzEscapeRedisGlob$$' -fuzztime=$(FUZZTIME)
	go test . -run '^$$' -fuzz='^FuzzDeleteByPrefixPattern$$' -fuzztime=$(FUZZTIME)
	go test . -run '^$$' -fuzz='^FuzzRedisConfigString$$' -fuzztime=$(FUZZTIME)
	go test . -run '^$$' -fuzz='^FuzzSieveCacheOperationSequence$$' -fuzztime=$(FUZZTIME)
	go test . -run '^$$' -fuzz='^FuzzGetOrLoadJSONCacheHit$$' -fuzztime=$(FUZZTIME)

lint-actions:
	go run github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION)

fmt:
	gofmt -w .
	goimports -w .

vet:
	go vet ./...

lint:
	golangci-lint run ./...

lint-fix:
	golangci-lint run --fix ./...

cover:
	go test -cover ./...

tidy:
	go mod tidy
	cd integration && go mod tidy
