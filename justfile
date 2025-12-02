set shell := ["/usr/bin/env", "bash", "-c"]

default: fmt up

fmt:
    go fmt ./...

up:
    docker compose --profile dev up

up-local:
    docker compose --profile local up

down *FLAGS:
    #!/usr/bin/env bash
    cmd="docker compose --profile dev down --remove-orphans"
    if [[ "{{FLAGS}}" == *"--rmi"* ]]; then
        cmd="${cmd} --rmi local"
    fi
    eval $cmd

down-local:
    docker compose --profile local down --rmi local --remove-orphans

build:
    go build -o ~/go/bin/controller ./cmd/main.go

ar-push TAG: build-docker (tag TAG) (push TAG)

build-docker:
    docker build -t controller:dev -f Dockerfile .

run-docker:
    docker run -it --rm --name controller -p 8080:8080 -v /var/run/docker.sock:/var/run/docker.sock controller:dev

tag TAG:
    docker tag controller:dev us-central1-docker.pkg.dev/local-first-476300/open-source-application-images/controller:{{TAG}}

push TAG:
    docker push us-central1-docker.pkg.dev/local-first-476300/open-source-application-images/controller:{{TAG}}

tidy:
    go mod tidy

add PACKAGE:
    go get -u {{PACKAGE}}
