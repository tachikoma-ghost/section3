BINARY := bin/section3
VERSION := $(shell cat VERSION 2>/dev/null || echo dev)

.PHONY: build clean

build:
	mkdir -p bin
	go build -o $(BINARY) .

clean:
	rm -rf bin
