FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -o /notifynyc

FROM alpine:3.21
RUN mkdir /log
COPY --from=build /notifynyc /notifynyc
CMD ["/notifynyc"]
