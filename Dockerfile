FROM golang:1.27 AS build

WORKDIR /go/src/app
COPY go.mod go.sum ./
COPY api/go.mod api/go.sum ./api/
RUN go mod download
COPY . .

RUN go vet -v ./...
RUN go test -v ./...

ARG VERSION=dev
RUN mkdir -p /go/bin/app && \
    CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /go/bin/app/ ./cmd/...

FROM gcr.io/distroless/static-debian13

COPY --from=build /go/bin/app /
ENV BEENFAR_DB=sqlite:///data/beenfar.db
VOLUME /data
EXPOSE 8080
CMD ["/beenfard"]
