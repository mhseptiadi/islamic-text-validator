# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /bin/validator-api ./cmd/validator-api

FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app
COPY --from=builder --chown=nonroot:nonroot /bin/validator-api /app/validator-api
COPY --chown=nonroot:nonroot data/generated/data.db /app/data/generated/data.db

ENV DB_PATH=/app/data/generated/data.db
ENV ADDR=:8080
EXPOSE 8080

ENTRYPOINT ["/app/validator-api"]
