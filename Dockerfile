FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.mod main.go ./
RUN go build -o /auto_typer .

FROM alpine:3.21
COPY --from=builder /auto_typer /usr/local/bin/auto_typer
ENTRYPOINT ["auto_typer"]
