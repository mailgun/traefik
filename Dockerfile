# WEBUI
FROM node:20.11 as webui

ENV WEBUI_DIR /src/webui
RUN mkdir -p $WEBUI_DIR

COPY ./webui/ $WEBUI_DIR/

WORKDIR $WEBUI_DIR

RUN yarn install
RUN yarn build

# BUILD
FROM golang:1.22-alpine as gobuild

RUN apk --no-cache --no-progress add git mercurial bash gcc musl-dev curl tar ca-certificates tzdata \
    && update-ca-certificates \
    && rm -rf /var/cache/apk/*

WORKDIR /go/src/github.com/mailgun/traefik

# Download go modules
COPY go.mod .
COPY go.sum .
RUN GO111MODULE=on GOPROXY=https://proxy.golang.org go mod download

COPY . /go/src/github.com/mailgun/traefik

RUN rm -rf /go/src/github.com/mailgun/traefik/webui/static/
COPY --from=webui /src/webui/static/ /go/src/github.com/mailgun/traefik/webui/static/

RUN go generate
RUN CGO_ENABLED=0 GOGC=off go build -ldflags "-s -w" -o dist/traefik ./cmd/traefik

## IMAGE
FROM alpine:3.14

RUN apk --no-cache --no-progress add bash curl ca-certificates tzdata \
    && update-ca-certificates \
    && rm -rf /var/cache/apk/*

COPY --from=gobuild /go/src/github.com/mailgun/traefik/dist/traefik /

EXPOSE 80
VOLUME ["/tmp"]

ENTRYPOINT ["/traefik"]
