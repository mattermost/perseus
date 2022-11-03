
FROM golang:1.19.3-alpine3.15 AS builder

RUN apk add --update --no-cache ca-certificates bash make gcc musl-dev git openssh wget curl

WORKDIR /go/src/perseus

COPY go.mod .
COPY go.sum .
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build ./cmd/perseus

#####

FROM gcr.io/distroless/static-debian11

COPY --from=builder /go/src/perseus/perseus /perseus
COPY --from=builder /go/src/perseus/config/config.sample.json config/config.json

ENTRYPOINT [ "/perseus" ]
