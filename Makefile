# Makefile проекта mapsettings.

VERSION ?= dev
LDFLAGS  = -X main.version=$(VERSION)
BINARY   = mapsettings

.PHONY: all build test vet fmt clean cross

all: build

## build — нативная сборка в ./mapsettings
build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) .

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
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/mapsettings-windows-amd64-$(VERSION).exe .
	GOOS=darwin  GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/mapsettings-darwin-arm64-$(VERSION) .
	GOOS=darwin  GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/mapsettings-darwin-amd64-$(VERSION) .

## clean — удалить артефакты сборки
clean:
	rm -rf dist $(BINARY)
