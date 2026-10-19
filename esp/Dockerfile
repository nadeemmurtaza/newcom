FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/nmail-core .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=build /out/nmail-core ./nmail-core
COPY --from=build /src/public ./public
EXPOSE 8080 2525
ENTRYPOINT ["./nmail-core"]
