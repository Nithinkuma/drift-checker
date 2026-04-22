FROM golang:1.25-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /drift-checker ./cmd/server

FROM gcr.io/distroless/static-debian12

COPY --from=builder /drift-checker /drift-checker

EXPOSE 8080
ENTRYPOINT ["/drift-checker"]
