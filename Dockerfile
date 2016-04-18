FROM ubuntu:14.04

RUN apt-get install -y software-properties-common
RUN add-apt-repository ppa:ubuntu-lxc/lxd-stable
RUN apt-get update
RUN apt-get install -y golang git
RUN apt-get install -y libav-tools exiftool

RUN mkdir /govitra
RUN mkdir /go
ENV GOPATH=/go
ADD . /govitra

WORKDIR /govitra
RUN go get -d ./...

RUN mkdir -p /govitra/bin/temp
RUN mkdir -p /govitra/bin/serve

EXPOSE 8080

CMD ["bash", "/govitra/start-docker.sh"]
