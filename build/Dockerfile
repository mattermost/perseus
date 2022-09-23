
FROM golang:1.18.6-alpine3.15 AS builder

RUN apk add --update --no-cache ca-certificates bash make gcc musl-dev git openssh wget curl

WORKDIR /go/src/perseus

COPY go.mod .
COPY go.sum .
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build ./cmd/perseus

#####

FROM alpine:3.15

COPY --from=builder /go/src/perseus/perseus /perseus

ENTRYPOINT [ "/perseus", "-addr=:5433" ]
