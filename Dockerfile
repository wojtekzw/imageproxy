FROM google/golang
MAINTAINER wojtekz <wojtekz@wp.pl>

ADD . /go/src/github.com/wojtekzw/imageproxy
RUN go get github.com/wojtekzw/imageproxy/cmd/imageproxy

CMD []
ENTRYPOINT ["/go/bin/imageproxy"]

EXPOSE 8080
