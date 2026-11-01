# avmfork links go-algorand's packages, which need go-algorand's own build
# of libsodium. `make deps` fetches go-algorand at the pinned release, builds
# that library, and points a go.work file at the checkout.

GO_ALGORAND_COMMIT := 31186e99a69e74483ad95e9ce872903b74758abf # v5.0.5-stable, what MainNet runs
GA := third_party/go-algorand
UNAME_S := $(shell uname -s | tr A-Z a-z)
UNAME_M := $(shell uname -m | sed -e 's/x86_64/amd64/' -e 's/aarch64/arm64/')
SODIUM := $(GA)/crypto/libs/$(UNAME_S)/$(UNAME_M)/lib/libsodium.a

.PHONY: deps build test e2e difftest vet clean

deps: $(SODIUM) go.work

$(GA)/.git:
	mkdir -p third_party
	git clone --filter=blob:none https://github.com/algorand/go-algorand $(GA)
	git -C $(GA) checkout --quiet $(GO_ALGORAND_COMMIT)

$(SODIUM): $(GA)/.git
	$(MAKE) -C $(GA) libsodium

go.work: $(GA)/.git
	rm -f go.work go.work.sum
	go work init . ./$(GA)

build: deps
	go build -o bin/avmfork ./cmd/avmfork
	go build -o bin/avmfork-difftest ./cmd/avmfork-difftest

vet: deps
	go vet ./...

test: deps
	go test ./...

# Needs network access to a MainNet algod.
e2e: deps
	AVMFORK_E2E=1 go test -count=1 -v ./e2e/

difftest: build
	./bin/avmfork-difftest -blocks 20 -per-block 0 -out difftest-out

clean:
	rm -rf bin difftest-out go.work go.work.sum
