FROM golang:1.25-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY *.go ./

RUN CGO_ENABLED=0 GOOS=linux go build -o /jobs-api .

FROM scratch

COPY --from=builder /jobs-api /jobs-api

USER 65532:65532

EXPOSE 8080

ENTRYPOINT ["/jobs-api"]
