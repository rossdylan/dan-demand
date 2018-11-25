from golang:1.11.2-alpine3.8
workdir /go/src/github.com/rossdylan/dan-demand/
run apk add bash ca-certificates git gcc g++ libc-dev

env GO111MODULE=on
env GOOS=linux
env GOARCH=amd64

copy go.mod go.sum .
run go mod download

copy *.go .
run CGO_ENABLED=1 go build -a -tags netgo -ldflags '-w -extldflags "-static"'

from alpine:latest
run apk --no-cache add ca-certificates

workdir /root/
copy --from=0 /go/src/github.com/rossdylan/dan-demand/dan-demand .
VOLUME ["/config"]
CMD ["/root/dan-demand", "--alsologtostderr", "--dan-demand.config=/config/dan-demand.toml", "--v=2"]
