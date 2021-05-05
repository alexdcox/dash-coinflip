FROM golang:latest
WORKDIR /go/src/github.com/alexdcox/dash-coinflip
COPY . .
RUN apt-get update && apt-get install -y --fix-missing ca-certificates libsodium-dev libczmq-dev libzmq5
RUN GO111MODULE=on CGO_ENABLED=1 GOOS=linux go build -ldflags="-s -w" -a -installsuffix cgo -o ./app

FROM ewalletdev/gozmq
WORKDIR /app
ARG version=unspecified
LABEL version=$version
ENV DR_VERSION $version
RUN apt-get update && apt-get install -y --fix-missing ca-certificates libsodium-dev libczmq-dev libzmq5
COPY --from=0 /go/src/github.com/alexdcox/dash-coinflip/app /app/app
ENTRYPOINT ["./app"]