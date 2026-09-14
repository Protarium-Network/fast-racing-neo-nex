FROM golang:1.24-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o /out/fast-racing-neo-server .

FROM alpine:3.20

WORKDIR /app

COPY --from=build /out/fast-racing-neo-server .

# * Authentication and secure servers. Both are UDP; publishing them as TCP
# * silently produces a server nothing can reach.
EXPOSE 26500/udp
EXPOSE 26501/udp

# * Leaderboards and common data live here; mount a volume to keep them.
VOLUME /app/data

# * Docker's userland proxy rewrites the source address, which would make the
# * server reflect the proxy's address back to consoles instead of their own
# * and break peer-to-peer connections. Run with --network host, or make sure
# * the published UDP ports preserve the source address.
CMD ["./fast-racing-neo-server"]
