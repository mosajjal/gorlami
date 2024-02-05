FROM golang:1.21.6-alpine3.19

LABEL maintainer "Ali Mosajjal <hi@n0p.me>"
RUN apk add --no-cache git
RUN mkdir /gorlami
ADD . /gorlami
WORKDIR /gorlami
ENV CGO_ENABLED=0
RUN GOFLAGS=-buildvcs=false go build -ldflags "-s -w -X main.version=$(git describe --tags) -X main.commit=$(git rev-parse HEAD)" .
CMD ["/gorlami/gorlami"]

FROM scratch
COPY --from=0 /gorlami/gorlami /gorlami
ENTRYPOINT ["/gorlami"]