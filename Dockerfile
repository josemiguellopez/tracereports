# TraceReports: un único binario estático (Go sin CGO) sobre una imagen mínima sin shell.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY web.go ./
COPY cmd ./cmd
COPY internal ./internal
COPY web ./web
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/tracereports ./cmd \
 && mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/tracereports /tracereports
# /data pertenece al usuario sin privilegios: un volumen nuevo hereda estos permisos
COPY --from=build --chown=65532:65532 /out/data /data
ENV PORT=8080 DATA_DIR=/data TRACEREPORTS_RUNTIME=docker
VOLUME /data
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/tracereports"]
