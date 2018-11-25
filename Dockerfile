from golang:1.11.2
workdir /go/src/github.com/rossdylan/dan-demand/
env GO111MODULE=on
env GOOS=linux

copy go.mod go.sum .
run go mod download

copy *.go .
run go build -a

from alpine:latest
run apk --no-cache add ca-certificates
workdir /root/
copy --from=0 /go/src/github.com/rossdylan/dan-demand/dan-demand .
CMD ["./dan-demand", "--alsologtostderr", "--dan-demand.config", "/etc/dan-demand.toml", "-v", "2"]
