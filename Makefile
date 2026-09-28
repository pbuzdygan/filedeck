.PHONY: build test race vet fuzz ui-test
build:
	go build -trimpath -o bin/filedeck ./cmd/filedeck
test:
	go test -count=1 -timeout=60s ./...
race:
	go test -race -count=1 -timeout=120s ./...
vet:
	go vet ./...
fuzz:
	go test ./internal/storage -run='^$$' -fuzz=FuzzValidPath -fuzztime=10s -parallel=2
# Browser smoke test on a throwaway instance; prebuilt Playwright image with a memory cap
# (never run it together with an image build — the dev host has 6 GB RAM and no swap).
ui-test:
	docker run --rm --network host --ipc=host --memory 2g --cpus 2 -e PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1 \
	  -e FILEDECK_URL -e FILEDECK_ADMIN -e FILEDECK_PASSWORD -v "$(CURDIR)/test/ui:/w" -w /w mcr.microsoft.com/playwright:v1.56.0-noble \
	  sh -c 'npm init -y >/dev/null && npm i -s playwright@1.56.0 && node smoke.mjs'
