# Makefile проекта mapsettings.

VERSION ?= dev
LDFLAGS  = -X main.version=$(VERSION)
BINARY   = mapsettings

.PHONY: all build build-debug dbg-order dbg-index test vet fmt clean cross

all: build

## build — нативная сборка в ./mapsettings
build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) .

## build-debug — сборка с отладочной нумерацией элементов интерфейса (№N)
build-debug:
	go build -ldflags "$(LDFLAGS) -X main.debug=true" -o $(BINARY) .

## dbg-order — дополнить фиксированный порядок номеров новыми id (append-only)
dbg-order: build
	./$(BINARY) -gen-dbgorder

## dbg-index — обновить индекс отладочных номеров (.kilo/dbg-index.txt)
dbg-index: build
	@mkdir -p .kilo
	./$(BINARY) -dbgindex > .kilo/dbg-index.txt

## test — тесты
test:
	go test ./...

## vet — статический анализ
vet:
	go vet ./...

## fmt — форматирование
fmt:
	gofmt -w *.go

## cross — кросс-сборки в dist/ (linux/windows/darwin, без внешних зависимостей)
cross:
	mkdir -p dist
	GOOS=linux   GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/mapsettings-linux-amd64-$(VERSION) .
	GOOS=windows GOARCH=386   go build -ldflags "$(LDFLAGS)" -o dist/mapsettings-windows-386-$(VERSION).exe .
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/mapsettings-windows-amd64-$(VERSION).exe .
	GOOS=darwin  GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/mapsettings-darwin-amd64-$(VERSION) .
	GOOS=darwin  GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/mapsettings-darwin-arm64-$(VERSION) .

## clean — удалить артефакты сборки
clean:
	rm -rf dist $(BINARY)
