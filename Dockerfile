FROM ewalletdev/gozmq
WORKDIR /go/src/github.com/alexdcox/dash-coinflip
COPY . .
RUN go build -ldflags="-s -w" -o ./app

FROM ewalletdev/gozmq
WORKDIR /app
ARG version=unspecified
LABEL version=$version
ENV DR_VERSION $version
RUN apt-get update && apt-get upgrade -y --fix-missing
COPY --from=0 /go/src/github.com/alexdcox/dash-coinflip/app /app/app
ENTRYPOINT ["./app"]