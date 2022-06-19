FROM golang:1.18 as build

WORKDIR /go/src/app
COPY . .

RUN go mod download
RUN go vet -v ./...
RUN go test -v ./...

RUN mkdir -p /go/bin/app

RUN CGO_ENABLED=0 go build -o /go/bin/app/ /go/src/app/cmd/*

FROM gcr.io/distroless/static-debian11

COPY --from=build /go/bin/app /
CMD ["/beenfard"]
