VERSION=0.6

.PHONY: build test

build: test
	go build -o builds/imageproxy -ldflags "-X main.BuildDate=`date -u '+%Y-%m-%d_%I:%M:%S%p'` -X main.GitHash=`git rev-parse HEAD` -X main.Version=${VERSION}" 

test:
	go test -v ../..
	go test -v .

build-linux: test 	
	GOOS=linux go build -o builds/imageproxy_linux -ldflags "-X main.BuildDate=`date -u '+%Y-%m-%d_%I:%M:%S%p'` -X main.GitHash=`git rev-parse HEAD` -X main.Version=${VERSION}" 
