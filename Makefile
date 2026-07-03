.PHONY: all backend web embed test lint run docker clean

all: web embed backend         ## build console + embed + server binary

backend:                       ## build the Go server (backend/server)
	cd backend && go build -o server ./cmd/server

web:                           ## build the web console (frontend/dist)
	cd frontend && npm install && npm run build

embed:                         ## copy console build into the Go embed dir
	rm -rf backend/internal/console/dist
	cp -r frontend/dist backend/internal/console/dist

test:                          ## run backend tests
	cd backend && go test ./...

lint:                          ## run backend linters
	cd backend && go vet ./... && golangci-lint run ./... || true

run: backend                   ## run the server with the sample config
	cd backend && ./server -conf configs/config.yaml

docker:                        ## build the all-in-one image
	docker build -t ai-gateway:latest .

clean:
	rm -f backend/server backend/server.exe
	rm -rf frontend/dist out
