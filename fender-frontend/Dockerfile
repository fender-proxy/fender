FROM golang:1.21-alpine AS builder
RUN apk add --no-cache git
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "-s -w" -o /fender-frontend .

FROM scratch
COPY --from=builder /fender-frontend /fender-frontend
ENTRYPOINT ["/fender-frontend"]
