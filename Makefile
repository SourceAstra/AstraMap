.PHONY: build clean test vet

build:
	go build -ldflags="-s -w" -o amap ./cmd/amap

clean:
	rm -f amap

test:
	go test ./...

vet:
	go vet ./...
