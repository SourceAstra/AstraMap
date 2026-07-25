.PHONY: build build-static-linux release publish clean test vet

build:
	go build -ldflags="-s -w" -o amap ./cmd/amap

build-static-linux:
	./build.sh

release:
	./release.sh

publish:
	./publish.sh

clean:
	rm -f amap
	rm -rf dist/

test:
	go test ./...

vet:
	go vet ./...
