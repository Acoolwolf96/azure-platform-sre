FROM golang:1.25-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY main.go ./

RUN CGO_ENABLED=0 GOOS=linux go build -o /worker .

FROM scratch

COPY --from=builder /worker /worker

USER 65532:65532

ENTRYPOINT ["/worker"]
