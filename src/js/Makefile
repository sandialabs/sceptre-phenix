SHELL := /bin/bash

SOURCES := $(shell find ./src \( -name '*.js' -o -name '*.vue' \))

all: clean dist/index.html

clean:
	$(RM) -r dist
	$(RM) -r node_modules

dist/index.html: $(SOURCES)
	(npm install ; npm run build)
