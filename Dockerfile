FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /tg-secretary ./cmd/tg-secretary \
    && mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /tg-secretary /tg-secretary
# Pre-created so a mounted volume is writable by the nonroot user.
COPY --from=build --chown=nonroot:nonroot /out/data /app/data
WORKDIR /app
VOLUME /app/data
EXPOSE 8080
ENTRYPOINT ["/tg-secretary"]
CMD ["serve"]
