FROM golang:latest AS gowithzmq
RUN apt-get update
RUN apt-get install -y --fix-missing \
    ca-certificates \
    libsodium-dev \
    libczmq-dev \
    libzmq5

FROM gowithzmq AS builder
WORKDIR /go/src/github.com/alexdcox/dash-coinflip
COPY . .
RUN go build -ldflags="-s -w" -o ./app

FROM gowithzmq AS runner
WORKDIR /app
ARG version=unspecified
LABEL version=$version
ENV DR_VERSION $version
RUN apt-get update && apt-get upgrade -y --fix-missing
COPY --from=builder /go/src/github.com/alexdcox/dash-coinflip/app /app/app
ENTRYPOINT ["./app"]