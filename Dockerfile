FROM ubuntu:14.04

RUN apt-get update
RUN apt-get install -y golang git
RUN apt-get install -y libav-tools exiftool

RUN mkdir /govitra
ADD . /govitra
RUN go get github.com/aws/aws-sdk-go/...

EXPOSE 8080

CMD ["bash", "/govitra/start-docker.sh"]
