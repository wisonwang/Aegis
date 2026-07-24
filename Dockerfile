FROM golang:1.26 AS build
WORKDIR /src
ENV GOPROXY=https://goproxy.cn,direct
ENV GOSUMDB=sum.golang.google.cn
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/aegis ./cmd/aegis

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/aegis /app/aegis
COPY --from=build /src/config.json /app/config.json
EXPOSE 8080
ENTRYPOINT ["/app/aegis", "-config", "/app/config.json"]
