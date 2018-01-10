FROM ubuntu:17.10
MAINTAINER Wojciech Zwiefka <wojtekz@zwiefka.pl>

# Ignore APT warnings about not having a TTY
ENV DEBIAN_FRONTEND noninteractive

# Ensure UTF-8 locale
# RUN echo "LANG=\"en_GB.UTF-8\"" > /etc/default/locale
# RUN locale-gen en_GB.UTF-8
# RUN dpkg-reconfigure locales

RUN apt-get update
RUN apt-get install -y \
    wget \
    imagemagick \
    libvips libvips-dev libvips-tools \
    git

RUN wget -O /tmp/golang.tar.gz https://redirector.gvt1.com/edgedl/go/go1.9.2.linux-amd64.tar.gz
RUN tar -C /usr/local -xzvf /tmp/golang.tar.gz

ADD . /go/src/github.com/wojtekzw/imageproxy
RUN /usr/local/go/bin/go get github.com/wojtekzw/imageproxy/cmd/imageproxy
RUN /usr/local/go/bin/go build github.com/wojtekzw/imageproxy/cmd/imageproxy

CMD []
# ENTRYPOINT ["/go/src/github.com/wojtekzw/imageproxy/cmd/imageproxy/imageproxy"]

EXPOSE 8080

