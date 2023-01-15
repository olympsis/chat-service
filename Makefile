VERSION := v0.1.6
PROJECT_NAME := chat
SERVICE_NAME := chat-service
PKG := "olympsis-services/$(PROJECT_NAME)"
PKG_LIST := $( go list ${PKG}/... | grep -v /vendor/)
GO_FILES := $( find . -name '*.go' | grep -v /vendor/ | grep -v _test.go)

.PHONY: all dep build clean test coverage coverhtml lint

all: build

lint: ## Lint the files
	golint -set_exit_status ${PKG_LIST}

test: ## Run unittests
	go test -short ${PKG_LIST}

race: dep ## Run data race detector
	go test -race -short ${PKG_LIST}

msan: dep ## Run memory sanitizer
	go test -msan -short ${PKG_LIST}

dep: ## Get the dependencies
	go get -v -d ./...

build: dep ## Build the binary file
	go build -v $(PKG) 

docker:
	docker build . -t $(SERVICE_NAME)
	docker rmi $$(docker images -f "dangling=true" -q) --force
	
run:
	docker run --network d35064053a0f -p 7011:7011 $(SERVICE_NAME)

git:
	git add .
	git tag $(VERSION)
	git commit -m "added a new version. v0.1.6"
	git push

clean: ## Remove previous build
	rm -f $(PROJECT_NAME)
