.PHONY: build build-static-linux clean test vet

build:
	go build -ldflags="-s -w" -o amap ./cmd/amap

build-static-linux:
	./build.sh

clean:
	rm -f amap

test:
	go test ./...

vet:
	go vet ./...
