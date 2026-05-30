BINARY := myterm
PKG    := ./cmd/myterm

.PHONY: tidy run build windows linux macos clean

tidy:
	go mod tidy

run:
	go run $(PKG)

build:
	go build -o $(BINARY) $(PKG)

windows:
	GOOS=windows GOARCH=amd64 go build -o dist/$(BINARY).exe $(PKG)

linux:
	GOOS=linux GOARCH=amd64 go build -o dist/$(BINARY)-linux $(PKG)

macos:
	GOOS=darwin GOARCH=arm64 go build -o dist/$(BINARY)-macos $(PKG)

clean:
	rm -rf dist $(BINARY) $(BINARY).exe
