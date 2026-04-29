.PHONY: build run test fmt vet install uninstall clean

BIN    := brief
PREFIX ?= /usr/local

build:
	CGO_ENABLED=1 go build -o $(BIN) .

run: build
	./$(BIN)

test:
	go test ./...

fmt:
	go fmt ./...

vet:
	go vet ./...

install: build
	install -d $(PREFIX)/bin
	install -m 0755 $(BIN) $(PREFIX)/bin/$(BIN)
	@echo "Installed to $(PREFIX)/bin/$(BIN)"

uninstall:
	rm -f $(PREFIX)/bin/$(BIN)
	@echo "Uninstalled."

clean:
	rm -f $(BIN)
