FROM golang:alpine AS build
ARG appVersion=0.0.0
RUN apk add --update --no-cache ca-certificates git
COPY . $GOPATH/src/github.com/onetwotrip/nodeup/
WORKDIR $GOPATH/src/github.com/onetwotrip/nodeup/
RUN go get -d -v
RUN GOARCH=amd64 CGO_ENABLED=0 GOOS=linux go build -ldflags="-X 'main.AppVersion=$appVersion'" -o /go/bin/nodeup
FROM busybox
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /go/bin/nodeup /bin/
ENTRYPOINT ["nodeup"]